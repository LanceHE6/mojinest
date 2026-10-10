#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod importer;
mod wx_decrypt;

use arboard::{Clipboard, ImageData};
use directories::ProjectDirs;
use importer::{detect_platform_sources, import_directory, DetectedPlatform, ImportSummary};
use rfd::FileDialog;
use serde::{Deserialize, Serialize};
use std::borrow::Cow;
use std::fs;
use std::path::PathBuf;
use tauri::menu::{Menu, MenuItem};
use tauri::tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent};
use tauri::{AppHandle, Emitter, Manager, Window};
use tauri_plugin_global_shortcut::{GlobalShortcutExt, Shortcut};

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct DesktopConfig {
    pub server_url: String,
    pub token: String,
    pub hotkey: String,
}

impl Default for DesktopConfig {
    fn default() -> Self {
        Self {
            server_url: "http://192.168.1.4:18642".into(),
            token: "mojinest-test-2026".into(),
            hotkey: "Ctrl+Alt+E".into(),
        }
    }
}

fn config_path() -> PathBuf {
    if let Some(proj_dirs) = ProjectDirs::from("cn", "hycer", "mojinest") {
        let dir = proj_dirs.config_dir();
        fs::create_dir_all(dir).ok();
        dir.join("desktop.json")
    } else {
        PathBuf::from("desktop.json")
    }
}

fn load_config() -> DesktopConfig {
    let p = config_path();
    if let Ok(content) = fs::read_to_string(&p) {
        if let Ok(cfg) = serde_json::from_str(&content) {
            return cfg;
        }
    }
    let def = DesktopConfig::default();
    if let Ok(ser) = serde_json::to_string_pretty(&def) {
        fs::write(&p, ser).ok();
    }
    def
}

#[tauri::command]
fn get_desktop_config() -> DesktopConfig {
    load_config()
}

#[tauri::command]
fn save_desktop_config(app: AppHandle, cfg: DesktopConfig) -> Result<(), String> {
    let p = config_path();
    let ser = serde_json::to_string_pretty(&cfg).map_err(|e| e.to_string())?;
    fs::write(&p, ser).map_err(|e| e.to_string())?;

    // 重新注册全局快捷键
    if let Ok(s) = cfg.hotkey.parse::<Shortcut>() {
        let _ = app.global_shortcut().unregister_all();
        let h_clone = app.clone();
        let _ = app.global_shortcut().on_shortcut(s, move |_app, _shortcut, event| {
            if event.state() == tauri_plugin_global_shortcut::ShortcutState::Pressed {
                toggle_picker_window(&h_clone);
            }
        });
    }

    Ok(())
}

#[tauri::command]
fn open_settings_modal(window: Window) {
    window.emit("open-settings", ()).ok();
    window.show().ok();
    window.unminimize().ok();
    window.set_focus().ok();
}

#[tauri::command]
fn hide_window(window: Window) {
    window.hide().ok();
}

#[tauri::command]
fn get_detected_platforms() -> Vec<DetectedPlatform> {
    detect_platform_sources()
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct ImportProgressEvent {
    pub current: usize,
    pub total: usize,
    pub current_name: String,
    pub status: String,
}

#[tauri::command]
async fn import_files_from_paths(app: AppHandle, paths: Vec<String>) -> Result<ImportSummary, String> {
    let cfg = load_config();
    let client = reqwest::Client::new();
    let mut uploaded_count = 0;
    let mut skipped_count = 0;
    let mut failed_count = 0;
    let total_scanned = paths.len();

    for (idx, path_str) in paths.iter().enumerate() {
        let p = std::path::Path::new(path_str);
        if !p.exists() || !p.is_file() {
            continue;
        }

        let fname = p.file_name().unwrap_or_default().to_string_lossy().to_string();
        app.emit("import-progress", ImportProgressEvent {
            current: idx + 1,
            total: total_scanned,
            current_name: fname.clone(),
            status: "uploading".into(),
        }).ok();

        let bytes = match fs::read(p) {
            Ok(b) => b,
            Err(_) => {
                failed_count += 1;
                continue;
            }
        };

        let mime = match p.extension().and_then(|e| e.to_str()).unwrap_or("") {
            "png" => "image/png",
            "gif" => "image/gif",
            "jpg" | "jpeg" => "image/jpeg",
            "webp" => "image/webp",
            _ => "application/octet-stream",
        };

        match importer::upload_image(
            &client,
            &cfg.server_url,
            &cfg.token,
            &fname,
            bytes,
            mime,
            "drag-drop",
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
            "拖拽导入完成：共处理 {} 个文件，成功 {} 张，跳过/重复 {} 张，失败 {} 个",
            total_scanned, uploaded_count, skipped_count, failed_count
        ),
    })
}

#[tauri::command]
async fn pick_and_import_local_files(app: AppHandle) -> Result<ImportSummary, String> {
    let cfg = load_config();
    let files = FileDialog::new()
        .add_filter("Images", &["png", "jpg", "jpeg", "gif", "webp"])
        .pick_files();

    let files = match files {
        Some(f) => f,
        None => return Err("未选择任何文件".into()),
    };

    let client = reqwest::Client::new();
    let mut uploaded_count = 0;
    let mut skipped_count = 0;
    let mut failed_count = 0;
    let total_scanned = files.len();

    for (idx, p) in files.iter().enumerate() {
        let fname = p.file_name().unwrap_or_default().to_string_lossy().to_string();
        app.emit("import-progress", ImportProgressEvent {
            current: idx + 1,
            total: total_scanned,
            current_name: fname.clone(),
            status: "uploading".into(),
        }).ok();

        let bytes = match fs::read(p) {
            Ok(b) => b,
            Err(_) => {
                failed_count += 1;
                continue;
            }
        };
        let mime = match p.extension().and_then(|e| e.to_str()).unwrap_or("") {
            "png" => "image/png",
            "gif" => "image/gif",
            "jpg" | "jpeg" => "image/jpeg",
            "webp" => "image/webp",
            _ => "application/octet-stream",
        };

        match importer::upload_image(
            &client,
            &cfg.server_url,
            &cfg.token,
            &fname,
            bytes,
            mime,
            "local",
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
            "本地导入完成：共选择 {} 个文件，成功 {} 张，跳过 {} 张，失败 {} 个",
            total_scanned, uploaded_count, skipped_count, failed_count
        ),
    })
}

#[tauri::command]
async fn run_platform_import(app: AppHandle, dir_path: String, platform: String) -> Result<ImportSummary, String> {
    let cfg = load_config();
    import_directory(app, cfg.server_url, cfg.token, dir_path, platform).await
}

#[tauri::command]
async fn run_wechat_decrypt_export(app: AppHandle) -> Result<wx_decrypt::WeChatExportSummary, String> {
    use walkdir::WalkDir;
    let cfg = load_config();
    let client = reqwest::Client::new();

    // 寻找本机微信数据目录
    let mut wx_dirs = Vec::new();
    if let Ok(userprofile) = std::env::var("USERPROFILE") {
        wx_dirs.push(PathBuf::from(&userprofile).join("Documents\\WeChat Files"));
    }
    wx_dirs.push(PathBuf::from("D:\\xwechat_files"));

    let mut found_account_dir = None;
    let mut target_wxid = String::new();

    for root in wx_dirs {
        if root.exists() {
            for entry in WalkDir::new(&root).max_depth(2).into_iter().filter_map(|e| e.ok()) {
                let name = entry.file_name().to_string_lossy();
                if name.starts_with("wxid_") {
                    let emo_dir = entry.path().join("business\\emoticon\\Persist");
                    if emo_dir.exists() {
                        let raw_name = name.to_string();
                        // 提取纯净 wxid (例如 wxid_rdqwsh14dmmy22_7f72 -> wxid_rdqwsh14dmmy22)
                        let clean_wxid = if let Some(idx) = raw_name.rfind('_') {
                            if idx > 5 {
                                raw_name[..idx].to_string()
                            } else {
                                raw_name
                            }
                        } else {
                            raw_name
                        };
                        target_wxid = clean_wxid;
                        found_account_dir = Some(entry.path().to_path_buf());
                        break;
                    }
                }
            }
        }
        if found_account_dir.is_some() {
            break;
        }
    }

    let account_dir = match found_account_dir {
        Some(d) => d,
        None => return Err("未找到本地微信 4.x 数据目录".into()),
    };

    let mut candidate_dirs = Vec::new();
    let persist_dir = account_dir.join("business\\emoticon\\Persist");
    let thumb_dir = account_dir.join("business\\emoticon\\Thumb");
    if thumb_dir.exists() {
        candidate_dirs.push(thumb_dir);
    }
    if persist_dir.exists() {
        candidate_dirs.push(persist_dir);
    }

    if candidate_dirs.is_empty() {
        return Err("未找到微信表情目录".into());
    }

    // 挑选一个样本文件用于验证解密密钥
    let mut sample_cipher_block = Vec::new();
    let mut encrypted_files = Vec::new();

    for dir in &candidate_dirs {
        for entry in WalkDir::new(dir).into_iter().filter_map(|e| e.ok()) {
            if entry.file_type().is_file() {
                if let Ok(meta) = entry.metadata() {
                    if meta.len() >= 16 && meta.len() % 16 == 0 {
                        let p = entry.into_path();
                        if sample_cipher_block.is_empty() {
                            if let Ok(bytes) = fs::read(&p) {
                                if bytes.len() >= 16 {
                                    sample_cipher_block = bytes[..16].to_vec();
                                }
                            }
                        }
                        encrypted_files.push(p);
                    }
                }
            }
        }
    }

    if sample_cipher_block.is_empty() || encrypted_files.is_empty() {
        return Err("微信表情目录中无有效加密文件".into());
    }

    // 扫描内存中的 seed
    let seed = wx_decrypt::scan_and_find_seed(&target_wxid, &sample_cipher_block)
        .ok_or_else(|| "未能从正在运行的微信内存中抓取到有效解密 Seed，请确保微信已正常登录！".to_string())?;

    let aes_key = wx_decrypt::derive_key(&seed, &target_wxid);
    let total_found = encrypted_files.len();
    let mut decrypted_count = 0;
    let mut failed_count = 0;

    for (idx, p) in encrypted_files.iter().enumerate() {
        let fname = p.file_name().unwrap_or_default().to_string_lossy().to_string();
        app.emit("import-progress", ImportProgressEvent {
            current: idx + 1,
            total: total_found,
            current_name: fname.clone(),
            status: "decrypting".into(),
        }).ok();

        let cipher_bytes = match fs::read(p) {
            Ok(b) => b,
            Err(_) => {
                failed_count += 1;
                continue;
            }
        };

        let decrypted_bytes = match wx_decrypt::decrypt_full_file(&aes_key, &cipher_bytes) {
            Ok(b) => b,
            Err(_) => {
                failed_count += 1;
                continue;
            }
        };

        let (ext, mime) = match wx_decrypt::is_valid_decrypted_header(&decrypted_bytes) {
            Some(pair) => pair,
            None => {
                failed_count += 1;
                continue;
            }
        };

        let upload_name = format!("{}_{}.{}", fname, idx + 1, ext);
        match importer::upload_image(
            &client,
            &cfg.server_url,
            &cfg.token,
            &upload_name,
            decrypted_bytes,
            mime,
            "wechat",
        )
        .await
        {
            Ok(_) => decrypted_count += 1,
            Err(_) => failed_count += 1,
        }
    }

    Ok(wx_decrypt::WeChatExportSummary {
        total_found,
        decrypted_count,
        failed_count,
        seed_used: seed,
        message: format!(
            "微信表情解密完成：共发现 {} 个加密文件，成功解密并入库 {} 张，失败 {} 个",
            total_found, decrypted_count, failed_count
        ),
    })
}

#[tauri::command]
fn start_drag(window: Window) {
    window.start_dragging().ok();
}

#[tauri::command]
fn open_web_browser(url: String) {
    open::that(url).ok();
}

#[tauri::command]
async fn copy_item_to_clipboard(file_url: String, ext: String, mime_type: String, scale_for_chat: bool) -> Result<(), String> {
    let client = reqwest::Client::new();
    let resp = client
        .get(&file_url)
        .send()
        .await
        .map_err(|e| format!("下载图片失败: {}", e))?;

    let bytes = resp
        .bytes()
        .await
        .map_err(|e| format!("读取图片流失败: {}", e))?;

    let is_gif = ext.eq_ignore_ascii_case("gif") || mime_type.contains("gif") || bytes.starts_with(b"GIF");

    // 如果指定缩放模式（左键单击默认启用），且图片为静态图且长边超过 240px，等比缩放至标准表情包大小
    let final_bytes = if scale_for_chat && !is_gif {
        if let Ok(dyn_img) = image::load_from_memory(&bytes) {
            let (w, h) = (dyn_img.width(), dyn_img.height());
            let max_dim = w.max(h);
            if max_dim > 240 {
                let scaled = dyn_img.resize(240, 240, image::imageops::FilterType::Lanczos3);
                let mut buf = std::io::Cursor::new(Vec::new());
                if scaled.write_to(&mut buf, image::ImageFormat::Png).is_ok() {
                    buf.into_inner()
                } else {
                    bytes.to_vec()
                }
            } else {
                bytes.to_vec()
            }
        } else {
            bytes.to_vec()
        }
    } else {
        bytes.to_vec()
    };

    #[cfg(target_os = "windows")]
    {
        use std::os::windows::process::CommandExt;
        use std::process::Command;

        let temp_dir = std::env::temp_dir().join("mojinest");
        fs::create_dir_all(&temp_dir).ok();
        let safe_ext = if is_gif { "gif" } else { "png" };
        let temp_file = temp_dir.join(format!("moji_{}.{}", std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).unwrap().as_millis(), safe_ext));
        fs::write(&temp_file, &final_bytes).map_err(|e| format!("写临时图片失败: {}", e))?;

        let ps_script = format!(
            r#"Add-Type -AssemblyName System.Windows.Forms, System.Drawing; $d = New-Object System.Windows.Forms.DataObject; $files = New-Object System.Collections.Specialized.StringCollection; $files.Add('{0}'); $d.SetFileDropList($files); $img = [System.Drawing.Image]::FromFile('{0}'); $d.SetImage($img); [System.Windows.Forms.Clipboard]::SetDataObject($d, $true); Write-Output 'ok';"#,
            temp_file.to_string_lossy().replace('\\', "\\\\")
        );

        let out = Command::new("powershell")
            .arg("-NoProfile")
            .arg("-NonInteractive")
            .arg("-STA")
            .arg("-Command")
            .arg(&ps_script)
            .creation_flags(0x08000000) // CREATE_NO_WINDOW
            .output()
            .map_err(|e| format!("调用剪贴板失败: {}", e))?;

        if !String::from_utf8_lossy(&out.stdout).to_lowercase().contains("ok") {
            return Err(format!("剪贴板设置未确认: {}", String::from_utf8_lossy(&out.stderr)));
        }
        return Ok(());
    }

    // 非 Windows 平台使用系统原生剪贴板位图
    let mut clipboard = Clipboard::new().map_err(|e| format!("打开系统剪贴板失败: {}", e))?;
    let img = image::load_from_memory(&final_bytes).map_err(|e| format!("图片解码失败: {}", e))?;
    let rgba = img.to_rgba8();
    let (width, height) = rgba.dimensions();

    let img_data = ImageData {
        width: width as usize,
        height: height as usize,
        bytes: Cow::Owned(rgba.into_raw()),
    };

    clipboard
        .set_image(img_data)
        .map_err(|e| format!("写入系统剪贴板失败: {}", e))?;

    Ok(())
}

// 显示主面板
fn show_picker_window(app: &AppHandle) {
    if let Some(window) = app.get_webview_window("main") {
        window.show().ok();
        window.unminimize().ok();
        window.set_focus().ok();
    }
}

// 弹出或收起浮窗
fn toggle_picker_window(app: &AppHandle) {
    if let Some(window) = app.get_webview_window("main") {
        if window.is_visible().unwrap_or(false) {
            window.hide().ok();
        } else {
            window.show().ok();
            window.unminimize().ok();
            window.set_focus().ok();
        }
    }
}

fn main() {
    let cfg = load_config();

    tauri::Builder::default()
        .plugin(tauri_plugin_global_shortcut::Builder::new().build())
        .plugin(tauri_plugin_clipboard_manager::init())
        .invoke_handler(tauri::generate_handler![
            get_desktop_config,
            save_desktop_config,
            open_settings_modal,
            get_detected_platforms,
            pick_and_import_local_files,
            import_files_from_paths,
            run_platform_import,
            run_wechat_decrypt_export,
            hide_window,
            start_drag,
            open_web_browser,
            copy_item_to_clipboard
        ])
        .setup(move |app| {
            let handle = app.handle().clone();

            // 1. 初始化系统托盘图标
            let show_i = MenuItem::with_id(app, "show", "显示", true, None::<&str>)?;
            let settings_i = MenuItem::with_id(app, "settings", "设置", true, None::<&str>)?;
            let quit_i = MenuItem::with_id(app, "quit", "退出", true, None::<&str>)?;
            let menu = Menu::with_items(app, &[&show_i, &settings_i, &quit_i])?;

            let icon_bytes = include_bytes!("../icons/32x32.png");
            let img = image::load_from_memory(icon_bytes).unwrap();
            let rgba = img.to_rgba8();
            let (w, h) = rgba.dimensions();
            let tray_icon = tauri::image::Image::new_owned(rgba.into_raw(), w, h);

            let _tray = TrayIconBuilder::new()
                .icon(tray_icon)
                .menu(&menu)
                .show_menu_on_left_click(false) // 关键：左键点击不要触发右键上下文菜单，右键才弹菜单
                .tooltip("Mojinest")
                .on_menu_event(move |app, event| match event.id.as_ref() {
                    "quit" => {
                        std::process::exit(0);
                    }
                    "show" => {
                        show_picker_window(app);
                    }
                    "settings" => {
                        if let Some(win) = app.get_webview_window("main") {
                            win.eval("window.__openSettingsModal && window.__openSettingsModal()").ok();
                            win.show().ok();
                            win.unminimize().ok();
                            win.set_focus().ok();
                        }
                    }
                    _ => {}
                })
                .on_tray_icon_event(|tray, event| {
                    if let TrayIconEvent::Click {
                        button: MouseButton::Left,
                        button_state: MouseButtonState::Up,
                        ..
                    } = event
                    {
                        // 只有左键鼠标按键完全释放 (Up) 时才执行显示，避免瞬间触发两次导致立即消失
                        show_picker_window(tray.app_handle());
                    }
                })
                .build(app)?;

            // 2. 注册全局快捷键 (默认 Ctrl+Alt+M)
    let shortcut: Result<Shortcut, _> = cfg.hotkey.parse();
    if let Ok(s) = shortcut {
        let h_clone = handle.clone();
        app.global_shortcut().on_shortcut(s, move |_app, _shortcut, event| {
            if event.state() == tauri_plugin_global_shortcut::ShortcutState::Pressed {
                toggle_picker_window(&h_clone);
            }
        })?;
    }

            // 3. 窗口支持按 Esc 隐藏，避免无脑强行 blur 关窗
            // 外部点击收起将在前端处理，方便更精确地判断
            let _ = &app;

            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("运行 Tauri 失败");
}
