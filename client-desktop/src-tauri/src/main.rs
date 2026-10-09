#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod importer;

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

#[tauri::command]
async fn pick_and_import_local_files() -> Result<ImportSummary, String> {
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

    for p in files {
        let bytes = match fs::read(&p) {
            Ok(b) => b,
            Err(_) => {
                failed_count += 1;
                continue;
            }
        };
        let fname = p.file_name().unwrap_or_default().to_string_lossy().to_string();
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
async fn run_platform_import(dir_path: String, platform: String) -> Result<ImportSummary, String> {
    let cfg = load_config();
    import_directory(cfg.server_url, cfg.token, dir_path, platform).await
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
async fn copy_item_to_clipboard(file_url: String, _mime_type: String) -> Result<(), String> {
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

    let mut clipboard = Clipboard::new().map_err(|e| format!("打开系统剪贴板失败: {}", e))?;

    // 解码图片为 RGBA 格式并直接写入系统原生剪贴板
    let img = image::load_from_memory(&bytes).map_err(|e| format!("图片解码失败: {}", e))?;
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
            run_platform_import,
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

            let _tray = TrayIconBuilder::new()
                .icon(app.default_window_icon().unwrap().clone())
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
