use aes::cipher::{block_padding::Pkcs7, BlockDecryptMut, KeyIvInit};
use md5::{Digest, Md5};
use serde::{Deserialize, Serialize};
use std::fs;
use std::path::{Path, PathBuf};
use walkdir::WalkDir;

#[cfg(target_os = "windows")]
use windows_sys::Win32::Foundation::{CloseHandle, HANDLE};
#[cfg(target_os = "windows")]
use windows_sys::Win32::System::Diagnostics::Debug::ReadProcessMemory;
#[cfg(target_os = "windows")]
use windows_sys::Win32::System::Memory::{
    VirtualQueryEx, MEMORY_BASIC_INFORMATION, MEM_COMMIT, PAGE_GUARD, PAGE_NOACCESS,
};
#[cfg(target_os = "windows")]
use windows_sys::Win32::System::Threading::{
    OpenProcess, PROCESS_QUERY_INFORMATION, PROCESS_VM_READ,
};

type Aes128CbcDec = cbc::Decryptor<aes::Aes128>;

#[derive(Debug, Serialize, Deserialize)]
pub struct WeChatExportSummary {
    pub total_found: usize,
    pub decrypted_count: usize,
    pub failed_count: usize,
    pub seed_used: String,
    pub message: String,
}

// 检查是否为合法的图片或 wxgf 动图头
pub fn is_valid_decrypted_header(data: &[u8]) -> Option<(&'static str, &'static str)> {
    if data.len() < 4 {
        return None;
    }
    if data.starts_with(b"\x89PNG") {
        return Some(("png", "image/png"));
    }
    if data.starts_with(b"GIF8") {
        return Some(("gif", "image/gif"));
    }
    if data.starts_with(b"\xFF\xD8\xFF") {
        return Some(("jpg", "image/jpeg"));
    }
    if data.starts_with(b"wxgf") {
        // 如果包含 wxgf (WXAM HEVC封装)，搜索是否内嵌了完整的 PNG 或 GIF 图像
        if let Some(pos) = data.windows(4).position(|w| w == b"\x89PNG") {
            return Some(("png", "image/png"));
        }
        if let Some(pos) = data.windows(4).position(|w| w == b"GIF8") {
            return Some(("gif", "image/gif"));
        }
        if let Some(pos) = data.windows(3).position(|w| w == b"\xFF\xD8\xFF") {
            return Some(("jpg", "image/jpeg"));
        }
        // 兜底返回 gif
        return Some(("gif", "image/gif"));
    }
    None
}

// 根据 seed 和 wxid 计算 16 字节 AES Key
pub fn derive_key(seed: &str, wxid: &str) -> [u8; 16] {
    let payload = format!("{}{}{}", seed, wxid, "EMOTICON");
    let mut hasher = Md5::new();
    hasher.update(payload.as_bytes());
    let res = hasher.finalize();
    let mut key = [0u8; 16];
    key.copy_from_slice(&res[..16]);
    key
}

// 解密单个块（16 字节），Key = IV
pub fn try_decrypt_block(key: &[u8; 16], ciphertext: &[u8]) -> Option<[u8; 16]> {
    if ciphertext.len() < 16 {
        return None;
    }
    let mut block = aes::cipher::generic_array::GenericArray::clone_from_slice(&ciphertext[..16]);
    let mut dec = Aes128CbcDec::new(key.into(), key.into());
    dec.decrypt_block_mut(&mut block);
    let mut res = [0u8; 16];
    res.copy_from_slice(&block);
    Some(res)
}

// 完整解密一个文件数据 (AES-128-CBC + PKCS7)
pub fn decrypt_full_file(key: &[u8; 16], ciphertext: &[u8]) -> Result<Vec<u8>, String> {
    if ciphertext.len() < 16 || ciphertext.len() % 16 != 0 {
        return Err("密文长度不是 16 的整数倍".into());
    }
    let mut buf = ciphertext.to_vec();
    let dec = Aes128CbcDec::new(key.into(), key.into());
    let decrypted = dec
        .decrypt_padded_mut::<Pkcs7>(&mut buf)
        .map_err(|e| format!("PKCS7 解密失败: {:?}", e))?;
    Ok(decrypted.to_vec())
}

// 获取系统上运行的 Weixin.exe PID 列表
#[cfg(target_os = "windows")]
pub fn get_weixin_pids() -> Vec<u32> {
    use windows_sys::Win32::System::Diagnostics::ToolHelp::{
        CreateToolhelp32Snapshot, Process32First, Process32Next, PROCESSENTRY32, TH32CS_SNAPPROCESS,
    };
    let mut pids = Vec::new();
    unsafe {
        let snap = CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS, 0);
        if snap != std::ptr::null_mut() && snap != -1isize as HANDLE {
            let mut pe: PROCESSENTRY32 = std::mem::zeroed();
            pe.dwSize = std::mem::size_of::<PROCESSENTRY32>() as u32;
            if Process32First(snap, &mut pe) != 0 {
                loop {
                    let exe_name = std::ffi::CStr::from_ptr(pe.szExeFile.as_ptr() as _)
                        .to_string_lossy();
                    if exe_name.eq_ignore_ascii_case("weixin.exe") {
                        pids.push(pe.th32ProcessID);
                    }
                    if Process32Next(snap, &mut pe) == 0 {
                        break;
                    }
                }
            }
            CloseHandle(snap);
        }
    }
    pids
}

#[cfg(not(target_os = "windows"))]
pub fn get_weixin_pids() -> Vec<u32> {
    Vec::new()
}

// 扫描微信内存寻找匹配的 seed
#[cfg(target_os = "windows")]
pub fn scan_and_find_seed(wxid: &str, sample_cipher_block: &[u8]) -> Option<String> {
    let pids = get_weixin_pids();
    for pid in pids {
        unsafe {
            let handle: HANDLE = OpenProcess(PROCESS_QUERY_INFORMATION | PROCESS_VM_READ, 0, pid);
            if handle == std::ptr::null_mut() {
                continue;
            }

            let mut addr: usize = 0;
            let mut mbi: MEMORY_BASIC_INFORMATION = std::mem::zeroed();
            let mbi_size = std::mem::size_of::<MEMORY_BASIC_INFORMATION>();

            while VirtualQueryEx(handle, addr as _, &mut mbi, mbi_size) == mbi_size {
                if mbi.State == MEM_COMMIT && (mbi.Protect & (PAGE_NOACCESS | PAGE_GUARD)) == 0 {
                    let size = mbi.RegionSize;
                    let step = 2 * 1024 * 1024;
                    let mut curr = addr;
                    let end = addr + size;

                    while curr < end {
                        let read_sz = (end - curr).min(step);
                        let mut buf = vec![0u8; read_sz];
                        let mut bytes_read = 0;

                        if ReadProcessMemory(handle, curr as _, buf.as_mut_ptr() as _, read_sz, &mut bytes_read) != 0 {
                            let slice = &buf[..bytes_read];
                            let mut start = None;
                            for (i, &b) in slice.iter().enumerate() {
                                if b >= b'0' && b <= b'9' {
                                    if start.is_none() {
                                        start = Some(i);
                                    }
                                } else {
                                    if let Some(s_idx) = start {
                                        let len = i - s_idx;
                                        if len >= 8 && len <= 12 {
                                            if let Ok(seed_str) = std::str::from_utf8(&slice[s_idx..i]) {
                                                let key = derive_key(seed_str, wxid);
                                                if let Some(dec_block) = try_decrypt_block(&key, sample_cipher_block) {
                                                    if is_valid_decrypted_header(&dec_block).is_some() {
                                                        CloseHandle(handle);
                                                        return Some(seed_str.to_string());
                                                    }
                                                }
                                            }
                                        }
                                        start = None;
                                    }
                                }
                            }
                        }
                        curr += read_sz;
                    }
                }
                addr += mbi.RegionSize;
            }
            CloseHandle(handle);
        }
    }
    None
}

#[cfg(not(target_os = "windows"))]
pub fn scan_and_find_seed(_wxid: &str, _sample_cipher_block: &[u8]) -> Option<String> {
    None
}
