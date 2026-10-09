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
pub fn sniff_image_type(bytes: &[u8]) -> Option<(&'static str, &'static str)> {
    if bytes.len() < 8 {
        return None;
    }
    if bytes.starts_with(b"\x89PNG\r\n\x1a\n") {
        return Some(("png", "image/png"));
    }
    if bytes.starts_with(b"GIF87a") || bytes.starts_with(b"GIF89a") || bytes.starts_with(b"GIF") {
        return Some(("gif", "image/gif"));
    }
    if bytes.starts_with(b"\xFF\xD8\xFF") {
        return Some(("jpg", "image/jpeg"));
    }
    if bytes.len() >= 12 && bytes.starts_with(b"RIFF") && &bytes[8..12] == b"WEBP" {
        return Some(("webp", "image/webp"));
    }
    None
}

// 自动检测各社交平台本地表情包路径
pub fn detect_platform_sources() -> Vec<DetectedPlatform> {
    let mut list = Vec::new();

    let userprofile = std::env::var("USERPROFILE").unwrap_or_else(|_| "C:\\Users\\Default".into());
    let doc_dir = PathBuf::from(&userprofile).join("Documents");

    // 1. QQNT (收藏表情 Ori 与 接收的表情 emoji-recv)
    let qq_tf = doc_dir.join("Tencent Files");
    if qq_tf.exists() {
        for entry in WalkDir::new(&qq_tf).max_depth(6).into_iter().filter_map(|e| e.ok()) {
            if !entry.file_type().is_dir() {
                continue;
            }
            let p = entry.path();
            let name = entry.file_name().to_string_lossy();
            // 收藏表情
            if name.eq_ignore_ascii_case("personal_emoji") || (name.eq_ignore_ascii_case("Ori") && p.to_string_lossy().contains("personal_emoji")) {
                let c = count_valid_images(p);
                if c > 0 {
                    list.push(DetectedPlatform {
                        id: "qq".into(),
                        name: format!("QQ 收藏表情 ({})", p.display()),
                        path: p.to_string_lossy().to_string(),
                        count: c,
                    });
                }
            } else if name.eq_ignore_ascii_case("emoji-recv") {
                let c = count_valid_images(p);
                if c > 0 {
                    list.push(DetectedPlatform {
                        id: "qq".into(),
                        name: format!("QQ 聊天表情缓存 ({})", p.display()),
                        path: p.to_string_lossy().to_string(),
                        count: c,
                    });
                }
            }
        }
    }

    // 2. 微信 (支持 D 盘 xwechat_files 及常见 WeChat Files)
    let wx_candidates = vec![
        PathBuf::from("D:\\xwechat_files"),
        doc_dir.join("WeChat Files"),
    ];
    for wx_root in wx_candidates {
        if wx_root.exists() {
            for entry in WalkDir::new(&wx_root).max_depth(6).into_iter().filter_map(|e| e.ok()) {
                if !entry.file_type().is_dir() {
                    continue;
                }
                let p = entry.path();
                let name = entry.file_name().to_string_lossy();
                if name.eq_ignore_ascii_case("emoticon") || name.eq_ignore_ascii_case("CustomEmotion") {
                    let c = count_valid_images(p);
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
    let wxwork_candidates = vec![
        PathBuf::from("D:\\WXWork"),
        doc_dir.join("WXWork"),
    ];
    for root in wxwork_candidates {
        if root.exists() {
            for entry in WalkDir::new(&root).max_depth(6).into_iter().filter_map(|e| e.ok()) {
                if !entry.file_type().is_dir() {
                    continue;
                }
                let p = entry.path();
                let name = entry.file_name().to_string_lossy();
                if name.eq_ignore_ascii_case("CustomEmotion") || name.eq_ignore_ascii_case("emoticon") {
                    let c = count_valid_images(p);
                    if c > 0 {
                        list.push(DetectedPlatform {
                            id: "wxwork".into(),
                            name: format!("企业微信表情库 ({})", p.display()),
                            path: p.to_string_lossy().to_string(),
                            count: c,
                        });
                    }
                }
            }
        }
    }

    list
}

// 快速校验目录内有效合法图片数量
fn count_valid_images(dir: &Path) -> usize {
    let mut count = 0;
    for entry in WalkDir::new(dir).max_depth(4).into_iter().filter_map(|e| e.ok()) {
        if entry.file_type().is_file() {
            if let Ok(meta) = entry.metadata() {
                if meta.len() >= 32 && meta.len() <= 30 * 1024 * 1024 {
                    if let Ok(mut f) = fs::File::open(entry.path()) {
                        use std::io::Read;
                        let mut buf = [0u8; 16];
                        if let Ok(n) = f.read(&mut buf) {
                            if sniff_image_type(&buf[..n]).is_some() {
                                count += 1;
                            }
                        }
                    }
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

        if meta.len() < 32 || meta.len() > 30 * 1024 * 1024 {
            continue;
        }

        let bytes = match fs::read(p) {
            Ok(b) => b,
            Err(_) => {
                failed_count += 1;
                continue;
            }
        };

        let (ext, mime) = match sniff_image_type(&bytes) {
            Some(pair) => pair,
            None => {
                // 非合法图片跳过
                skipped_count += 1;
                continue;
            }
        };

        total_scanned += 1;

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
            "导入完成：扫描到 {} 张有效图片，成功入库 {} 张，跳过/已存在 {} 张，失败 {} 个",
            total_scanned, uploaded_count, skipped_count, failed_count
        ),
    })
}
