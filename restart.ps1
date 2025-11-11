param(
    [int]$ApiPort = $(if ($env:NOFX_API_PORT) { [int]$env:NOFX_API_PORT } else { 8080 }),
    [int]$FrontPort = $(if ($env:NOFX_FRONT_PORT) { [int]$env:NOFX_FRONT_PORT } else { 3000 }),
    [string]$ConfigPath,
    [string]$PromptVariant = $(if ($env:NOFX_PROMPT_VARIANT) { $env:NOFX_PROMPT_VARIANT } else { 'zhugefan' }),
    [int]$ScanMinutesOverride = 0,
    [switch]$InlineRun
)

# 简化入口：一行命令重启后端与前端，并打开页面
# 使用：在项目根执行  .\restart.ps1  或传入 -ApiPort/-FrontPort/-ConfigPath/-PromptVariant 参数

try {
    [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
    $OutputEncoding = [Console]::OutputEncoding
    cmd /c chcp 65001 > $null 2>&1
} catch {}

# 若未提供 ConfigPath，自动选择最新配置路径：优先 trade\config.json，其次根目录 config.json
if (-not $ConfigPath -or $ConfigPath.Trim() -eq '') {
    $preferred = Join-Path $PSScriptRoot 'trade\config.json'
    $fallback  = Join-Path $PSScriptRoot 'config.json'
    if (Test-Path -LiteralPath $preferred) {
        $ConfigPath = $preferred
        Write-Host "Using config: $ConfigPath" -ForegroundColor Yellow
    } elseif (Test-Path -LiteralPath $fallback) {
        $ConfigPath = $fallback
        Write-Host "Using config (fallback): $ConfigPath" -ForegroundColor Yellow
    } else {
        Write-Host "Config file not found: trade\\config.json nor config.json under $PSScriptRoot" -ForegroundColor Red
        throw "Missing config.json; please place your latest trade/config.json or root config.json"
    }
}

# 固定提示词变体与绝对路径校验（确保始终使用 D:\TRAE\projerct\prompt\system_zhugefan.txt 与 user_zhugefan.txt）
$PromptSystemPath = Join-Path $PSScriptRoot 'prompt\system_zhugefan.txt'
$PromptUserPath   = Join-Path $PSScriptRoot 'prompt\user_zhugefan.txt'
if (-not (Test-Path -LiteralPath $PromptSystemPath)) {
    Write-Host "Prompt system file missing: $PromptSystemPath" -ForegroundColor Red
    throw "Missing prompt system_zhugefan.txt"
}
if (-not (Test-Path -LiteralPath $PromptUserPath)) {
    Write-Host "Prompt user file missing: $PromptUserPath" -ForegroundColor Red
    throw "Missing prompt user_zhugefan.txt"
}
Write-Host "Using prompt system: $PromptSystemPath" -ForegroundColor Yellow
Write-Host "Using prompt user:   $PromptUserPath" -ForegroundColor Yellow

$webDir = Join-Path $PSScriptRoot 'web'

function Resolve-ScanMinutes {
    param(
        [int]$Override,
        [string]$ConfigPath
    )
    if ($Override -gt 0) { return $Override }
    $envVal = $env:NOFX_SCAN_INTERVAL_MINUTES
    if ($envVal) {
        $parsed = 0
        [int]::TryParse($envVal, [ref]$parsed) | Out-Null
        if ($parsed -gt 0) { return $parsed }
    }
    if (Test-Path -LiteralPath $ConfigPath) {
        $rawJson = Get-Content -LiteralPath $ConfigPath -Raw
        if ($rawJson) {
            $cfgObj = $rawJson | ConvertFrom-Json
            if ($cfgObj -and $cfgObj.traders -and $cfgObj.traders.Count -gt 0) {
                return [int]$cfgObj.traders[0].scan_interval_minutes
            }
        }
    }
    return $null
}

function Stop-ServiceByPort {
    param(
        [int]$Port
    )
    try {
        $conns = Get-NetTCPConnection -LocalPort $Port -ErrorAction SilentlyContinue
        if ($conns) {
            $pids = $conns | Select-Object -ExpandProperty OwningProcess | Sort-Object -Unique
            foreach ($procId in $pids) {
                try {
                    $proc = Get-Process -Id $procId -ErrorAction SilentlyContinue
                    if ($proc) {
                        Write-Host "Stop process using port $Port, PID=$procId ($($proc.ProcessName))" -ForegroundColor Yellow
                        Stop-Process -Id $procId -Force -ErrorAction SilentlyContinue
                    }
                }
                catch {
                    # ignore individual process termination errors
                }
            }
        } else {
            Write-Host "Port $Port has no active connections." -ForegroundColor DarkGray
        }
    }
    catch {
        Write-Host "Cannot detect processes on port ${Port}: $($_.Exception.Message)" -ForegroundColor Red
    }
}

function Stop-ProjectRelatedProcesses {
    try {
        $projPath = [Regex]::Escape($PSScriptRoot)
        $list = Get-CimInstance Win32_Process | Where-Object { $_.CommandLine -and $_.CommandLine -match $projPath -and ($_.CommandLine -match 'vite|npm run dev|node|go run \.|go-build') }
        foreach ($item in $list) {
            try {
                Write-Host "Stop project-related process PID=$($item.ProcessId) cmd=$($item.CommandLine)" -ForegroundColor Yellow
                Stop-Process -Id $item.ProcessId -Force -ErrorAction SilentlyContinue
            } catch {}
        }
    } catch {}
}

function Wait-HttpOk {
    param(
        [string]$Url,
        [int]$TimeoutSec = 30
    )
    $deadline = (Get-Date).AddSeconds($TimeoutSec)
    while ((Get-Date) -lt $deadline) {
        try {
            $resp = Invoke-WebRequest -UseBasicParsing -Uri $Url -TimeoutSec 5
            if ($resp.StatusCode -eq 200) { return $true }
        } catch {}
        Start-Sleep -Milliseconds 600
    }
    return $false
}

# 1) 先停后端与前端（按端口+命令行兜底）
Write-Host "== Step 1: Stop existing services ==" -ForegroundColor Cyan
Stop-ServiceByPort -Port $ApiPort
Stop-ServiceByPort -Port $FrontPort
Stop-ProjectRelatedProcesses

# 2) 启动后端
Write-Host "== Step 2: Start backend ==" -ForegroundColor Cyan

# 解析 scan_interval_minutes 并导出环境变量，确保后端按最新配置生效
$scanMinutes = Resolve-ScanMinutes -Override $ScanMinutesOverride -ConfigPath $ConfigPath
if ($scanMinutes -and $scanMinutes -gt 0) {
    Write-Host "Resolved scan_interval_minutes=$scanMinutes (export to env)" -ForegroundColor Yellow
    Set-Item env:NOFX_SCAN_INTERVAL_MINUTES $scanMinutes
} else {
    Write-Host "No valid scan_interval_minutes; skip env override" -ForegroundColor DarkYellow
}

$backendCmd = "Set-Item env:API_PORT $ApiPort; Set-Item env:NOFX_PROMPT_VARIANT '$PromptVariant'; cd '$PSScriptRoot'; go run . '$ConfigPath'"
Write-Host "Backend command: $backendCmd" -ForegroundColor Green
$backendProc = Start-Process -FilePath "powershell" -ArgumentList "-NoProfile -ExecutionPolicy Bypass -Command $backendCmd" -WorkingDirectory $PSScriptRoot -PassThru
Write-Host "Backend started, PID=$($backendProc.Id). Waiting health check..." -ForegroundColor Green

$healthUrl = "http://localhost:$ApiPort/health"
if (-not (Wait-HttpOk -Url $healthUrl -TimeoutSec 40)) {
    Write-Host "Backend health check timeout: $healthUrl" -ForegroundColor Red
} else {
    Write-Host "Backend health OK: $healthUrl" -ForegroundColor Green
}

# 3) 启动前端
Write-Host "== Step 3: Start frontend ==" -ForegroundColor Cyan
if (-not (Test-Path $webDir)) {
    Write-Host "Frontend directory not found: $webDir" -ForegroundColor Red
} else {
    if (-not (Test-Path (Join-Path $webDir 'node_modules'))) {
        Write-Host "Installing frontend dependencies..." -ForegroundColor Yellow
        try {
            Push-Location $webDir
            npm install
            Pop-Location
        } catch {
            Write-Host "Dependency installation failed: $($_.Exception.Message)" -ForegroundColor Red
        }
    }

    $frontCmd = "cd '$webDir'; npm run dev -- --port $FrontPort"
    Write-Host "Frontend command: $frontCmd" -ForegroundColor Green
    $frontProc = Start-Process -FilePath "powershell" -ArgumentList "-NoProfile -ExecutionPolicy Bypass -Command $frontCmd" -WorkingDirectory $webDir -PassThru
    Write-Host "Frontend started, PID=$($frontProc.Id). Waiting availability..." -ForegroundColor Green

    $frontUrl = "http://localhost:$FrontPort/"
    if (-not (Wait-HttpOk -Url $frontUrl -TimeoutSec 30)) {
        Write-Host "Frontend wait timeout: $frontUrl" -ForegroundColor Yellow
    } else {
        Write-Host "Frontend is ready: $frontUrl" -ForegroundColor Green
    }

    # 4) 打开页面
    Write-Host "== Step 4: Open page ==" -ForegroundColor Cyan
    try { Start-Process $frontUrl } catch {}
}

Write-Host "Done: backend port=$ApiPort, frontend port=$FrontPort" -ForegroundColor Green