use crate::config::{ACTIVE_TRANSFERS, WARNING_LEVEL_FOR_BUFFER};
use serde::Serialize;
use sysinfo::{Pid, ProcessExt, System, SystemExt};
use tracing::{error, info, warn};

use libc;

/// 代理状态结构体（对应 Go 的 ProxyStatus）
#[derive(Debug, Serialize)]
pub struct ProxyStatus {
    pub active_connections: i64,
    pub total_mem: u64,
    pub process_mem: u64,
    pub avg_cache_per_conn: f64,
    pub cache_usage_ratio: f64,
}

/// 检查拥塞状态（对应 Go 的 CheckCongestion）
pub fn check_congestion(all_buffer_size: usize) -> ProxyStatus {
    let mut status = ProxyStatus {
        active_connections: 0,
        total_mem: 0,
        process_mem: 0,
        avg_cache_per_conn: 0.0,
        cache_usage_ratio: 0.0,
    };

    // 你设置的进程最大内存
    status.total_mem = get_process_max_memory();

    // 当前进程已用内存
    status.process_mem = get_process_used_memory();

    // 计算内存使用率
    let usage_ratio = if status.total_mem > 0 {
        status.process_mem as f64 / status.total_mem as f64
    } else {
        0.0
    };
    info!(
        "Proxy memory: {} MiB, Total memory: {} MiB, Ratio: {:.2}%",
        status.process_mem / 1024 / 1024,
        status.total_mem / 1024 / 1024,
        usage_ratio * 100.0
    );

    // 活跃连接数
    let active = ACTIVE_TRANSFERS.load(std::sync::atomic::Ordering::SeqCst);
    status.active_connections = active;

    if active <= 0 {
        return status;
    }

    // 平均每连接内存
    let per_conn_cache = all_buffer_size * 1024;
    let avg_cache = status.process_mem as f64 / active as f64;
    status.avg_cache_per_conn = avg_cache;
    status.cache_usage_ratio = avg_cache / per_conn_cache as f64;

    info!(
        "Active connections: {}, Average per-connection memory: {:.2} KB",
        active,
        avg_cache / 1024.0
    );

    // 拥塞警告
    if status.cache_usage_ratio > WARNING_LEVEL_FOR_BUFFER as f64 {
        warn!(
            "Potential congestion: average per-connection buffer near {} KB",
            per_conn_cache / 1024
        );
    }

    info!("Proxy status: {:?}", status);
    status
}

// 读取【你设置的进程最大内存】（字节）
fn get_process_max_memory() -> u64 {
    let mut rlim = libc::rlimit {
        rlim_cur: 0,
        rlim_max: 0,
    };

    unsafe {
        libc::getrlimit(libc::RLIMIT_AS, &mut rlim);
    }

    // 如果是无限制，返回 0（避免除0错误）
    if rlim.rlim_cur == libc::RLIM_INFINITY {
        0
    } else {
        rlim.rlim_cur as u64
    }
}

// 获取【当前进程已用内存】（字节）
pub fn get_process_used_memory() -> u64 {
    // 全局单例 System，避免重复创建
    use once_cell::sync::Lazy;
    static SYS: Lazy<tokio::sync::Mutex<sysinfo::System>> =
        Lazy::new(|| tokio::sync::Mutex::new(sysinfo::System::new_all()));

    tokio::runtime::Handle::current().block_on(async {
        let mut sys = SYS.lock().await;
        sys.refresh_processes();

        let pid = Pid::this();
        if let Some(process) = sys.process(pid) {
            process.memory() * 1024 // KB → Bytes
        } else {
            0
        }
    })
}