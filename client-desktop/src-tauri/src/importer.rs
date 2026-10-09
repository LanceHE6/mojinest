use serde::{Deserialize, Serialize};
use std::fs;
use std::path::{Path, PathBuf};
use walkdir::WalkDir;

#[derive(Debug, Serialize, Deserialize)]
pub struct ImportSummary {
    pub total_scanned: usize,
    pub uploaded_count: usize,
    pub skipped_count: usize,
    pub failed_count: usize,
    pub message: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct DetectedPlatform {
    pub id: String,
    pub name: String,
    pub path: String,
    pub count: usize,
}

// 检查文件头是否为有效图像 (PNG, GIF, JPEG, WEBP)
fn sniff_image_type(bytes: &[u8]) -> Option<(&'static str, &'static str)> {
    if bytes.len() < 8 {
        return None;
    }
    if &bytes[0..8] == b"\x89PNG\r\n\x1a\n" {
        return Some(("png", "image/png"));
    }
    if &bytes[0..3] == b"GIF" {
        return Some(("gif", "image/gif"));
    }
    if &bytes[0..3] == b"\xFF\xD8\xFF" {
        return Some(("jpg", "image/jpeg"));
    }
    if bytes.len() >= 12 && &bytes[0..4] == b"RIFF" && &bytes[8..12] == b"WEBP" {
        return Some(("webp", "image/webp"));
    }
    None
}

// 寻找常见的应用本地表情路径 (Windows & 全平台)
pub fn detect_platform_sources() -> Vec<DetectedPlatform> {
    let mut list = Vec::new();

    // 1. QQNT
    let mut qq_roots = Vec::new();
    if let Ok(userprofile) = std::env::var("USERPROFILE") {
        qq_roots.push(PathBuf::from(&userprofile).join("Documents\\Tencent Files"));
        qq_roots.push(PathBuf::from(&userprofile).join("AppData\\Roaming\\Tencent\\QQNT"));
    }
    // 兼顾用户 D 盘安装/数据盘
    qq_roots.push(PathBuf::from("D:\\QQNT"));
    qq_roots.push(PathBuf::from("D:\\Tencent Files"));

    for root in qq_roots {
        if root.exists() {
            for entry in WalkDir::new(&root).max_depth(5).into_iter().filter_map(|e| e.ok()) {
                let name = entry.file_name().to_string_lossy();
                if name.eq_ignore_ascii_case("Ori") || name.eq_ignore_ascii_case("CustomFace") {
                    let c = count_images(entry.path());
                    if c > 0 {
                        list.push(DetectedPlatform {
                            id: "qq".into(),
                            name: format!("QQ 表情库 ({})", entry.path().display()),
                            path: entry.path().to_string_lossy().to_string(),
                            count: c,
                        });
                    }
                }
            }
        }
    }

    // 2. 微信 (WeChat / xWeChat 4.x)
    let mut wx_roots = Vec::new();
    if let Ok(userprofile) = std::env::var("USERPROFILE") {
        wx_roots.push(PathBuf::from(&userprofile).join("Documents\\WeChat Files"));
        wx_roots.push(PathBuf::from(&userprofile).join("AppData\\Roaming\\Tencent\\WeChat"));
    }
    wx_roots.push(PathBuf::from("D:\\xwechat_files"));
    wx_roots.push(PathBuf::from("D:\\Weixin"));

    for root in wx_roots {
        if root.exists() {
            for entry in WalkDir::new(&root).max_depth(6).into_iter().filter_map(|e| e.ok()) {
                let p = entry.path();
                let name = entry.file_name().to_string_lossy();
                if name.eq_ignore_ascii_case("emoticon") || name.eq_ignore_ascii_case("CustomEmotion") {
                    let c = count_images(p);
                    if c > 0 {
                        list.push(DetectedPlatform {
                            id: "wechat".into(),
                            name: format!("微信表情库 ({})", p.display()),
                            path: p.to_string_lossy().to_string(),
                            count: c,
                        });
                    }
                }
            }
        }
    }

    // 3. 企业微信 (WXWork)
    let mut wxwork_roots = Vec::new();
    if let Ok(userprofile) = std::env::var("USERPROFILE") {
        wxwork_roots.push(PathBuf::from(&userprofile).join("Documents\\WXWork"));
        wxwork_roots.push(PathBuf::from(&userprofile).join("AppData\\Roaming\\Tencent\\WXWork"));
    }
    wxwork_roots.push(PathBuf::from("D:\\WXWork"));

    for root in wxwork_roots {
        if root.exists() {
            for entry in WalkDir::new(&root).max_depth(6).into_iter().filter_map(|e| e.ok()) {
                let name = entry.file_name().to_string_lossy();
                if name.eq_ignore_ascii_case("CustomEmotion") || name.eq_ignore_ascii_case("emoticon") {
                    let c = count_images(entry.path());
                    if c > 0 {
                        list.push(DetectedPlatform {
                            id: "wxwork".into(),
                            name: format!("企业微信表情库 ({})", entry.path().display()),
                            path: entry.path().to_string_lossy().to_string(),
                            count: c,
                        });
                    }
                }
            }
        }
    }

    list
}

fn count_images(dir: &Path) -> usize {
    let mut count = 0;
    for entry in WalkDir::new(dir).max_depth(4).into_iter().filter_map(|e| e.ok()) {
        if entry.file_type().is_file() {
            if let Ok(meta) = entry.metadata() {
                if meta.len() > 512 && meta.len() < 30 * 1024 * 1024 {
                    count += 1;
                }
            }
        }
    }
    count
}

// 上传单个图像到 Mojinest 服务端
pub async fn upload_image(
    client: &reqwest::Client,
    server_url: &str,
    token: &str,
    filename: &str,
    bytes: Vec<u8>,
    mime: &str,
    platform: &str,
) -> Result<bool, String> {
    let url = format!("{}/api/items", server_url.trim_end_matches('/'));

    let part = reqwest::multipart::Part::bytes(bytes)
        .file_name(filename.to_string())
        .mime_str(mime)
        .map_err(|e| e.to_string())?;

    let mut form = reqwest::multipart::Form::new()
        .part("file", part)
        .text("source_platform", platform.to_string());

    if !platform.is_empty() {
        form = form.text("tags", format!("{},导入", platform));
    }

    let mut req = client.post(&url).multipart(form);
    if !token.is_empty() {
        req = req.header("Authorization", format!("Bearer {}", token));
    }

    let res = req.send().await.map_err(|e| e.to_string())?;
    let status = res.status();
    if status == reqwest::StatusCode::CONFLICT {
        return Ok(false); // 重复表情跳过
    }
    if !status.is_success() {
        return Err(format!("HTTP {}", status));
    }

    Ok(true)
}

// 批量扫描并上传本地文件夹
pub async fn import_directory(
    server_url: String,
    token: String,
    dir_path: String,
    platform: String,
) -> Result<ImportSummary, String> {
    let client = reqwest::Client::new();
    let mut total_scanned = 0;
    let mut uploaded_count = 0;
    let mut skipped_count = 0;
    let mut failed_count = 0;

    for entry in WalkDir::new(&dir_path).into_iter().filter_map(|e| e.ok()) {
        if !entry.file_type().is_file() {
            continue;
        }

        let p = entry.path();
        let meta = match entry.metadata() {
            Ok(m) => m,
            Err(_) => continue,
        };

        if meta.len() < 512 || meta.len() > 30 * 1024 * 1024 {
            continue;
        }

        let bytes = match fs::read(p) {
            Ok(b) => b,
            Err(_) => {
                failed_count += 1;
                continue;
            }
        };

        total_scanned += 1;

        let (ext, mime) = match sniff_image_type(&bytes) {
            Some(pair) => pair,
            None => {
                // 如果不是标准图片头（例如某些加密或未知文件），跳过
                skipped_count += 1;
                continue;
            }
        };

        let fname = format!(
            "{}_{}.{}",
            entry.file_name().to_string_lossy(),
            total_scanned,
            ext
        );

        match upload_image(
            &client,
            &server_url,
            &token,
            &fname,
            bytes,
            mime,
            &platform,
        )
        .await
        {
            Ok(true) => uploaded_count += 1,
            Ok(false) => skipped_count += 1,
            Err(_) => failed_count += 1,
        }
    }

    Ok(ImportSummary {
        total_scanned,
        uploaded_count,
        skipped_count,
        failed_count,
        message: format!(
            "扫描完成：共处理 {} 个文件，成功上传 {} 张，跳过/重复 {} 张，失败 {} 个",
            total_scanned, uploaded_count, skipped_count, failed_count
        ),
    })
}
