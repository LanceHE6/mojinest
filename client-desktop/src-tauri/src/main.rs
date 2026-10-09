#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use arboard::{Clipboard, ImageData};
use directories::ProjectDirs;
use serde::{Deserialize, Serialize};
use std::borrow::Cow;
use std::fs;
use std::path::PathBuf;
use tauri::menu::{Menu, MenuItem};
use tauri::tray::{TrayIconBuilder, TrayIconEvent};
use tauri::{AppHandle, Manager, PhysicalPosition, Window};
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
            hotkey: "Ctrl+Alt+M".into(),
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
fn hide_window(window: Window) {
    window.hide().ok();
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

// 弹出浮窗（用户呼出时保持原位或居中，不再每次篡改位置到屏幕死角）
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
            hide_window,
            start_drag,
            open_web_browser,
            copy_item_to_clipboard
        ])
        .setup(move |app| {
            let handle = app.handle().clone();

            // 1. 初始化系统托盘图标
            let quit_i = MenuItem::with_id(app, "quit", "退出 Mojinest", true, None::<&str>)?;
            let show_i = MenuItem::with_id(app, "show", "呼出表情面板", true, None::<&str>)?;
            let menu = Menu::with_items(app, &[&show_i, &quit_i])?;

            let _tray = TrayIconBuilder::new()
                .icon(app.default_window_icon().unwrap().clone())
                .menu(&menu)
                .tooltip("Mojinest 贴图小窝")
                .on_menu_event(move |app, event| match event.id.as_ref() {
                    "quit" => {
                        std::process::exit(0);
                    }
                    "show" => {
                        toggle_picker_window(app);
                    }
                    _ => {}
                })
                .on_tray_icon_event(|tray, event| {
                    if let TrayIconEvent::Click { .. } = event {
                        toggle_picker_window(tray.app_handle());
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
