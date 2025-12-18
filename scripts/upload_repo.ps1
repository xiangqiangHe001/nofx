[CmdletBinding()]
param(
    [string]$RepoUrl,
    [string]$Branch = "main",
    [string]$CommitMessage = "chore: whitelist coins & upload (auto)",
    [switch]$Force,
    [string]$Proxy = $env:NOFX_PROXY,
    [switch]$EnableProxy,
    [switch]$GlobalProxy,
    [switch]$ProxyClear,
    [string]$Token
)

# Console encoding init (for click-to-run robustness)
try {
    [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
    $OutputEncoding = [Console]::OutputEncoding
    cmd /c chcp 65001 > $null 2>&1
} catch {}

# Project root (script is under scripts/)
$ProjectRoot = Split-Path $PSScriptRoot -Parent
Set-Location $ProjectRoot
Write-Verbose "Project root: $ProjectRoot"

# Derive RepoUrl for click-to-run if not provided (skip when only clearing proxy)
if (-not $RepoUrl -and -not $ProxyClear) {
    $originUrl = ''
    try { $originUrl = & git remote get-url origin 2>$null } catch {}
    if ($originUrl) {
        $RepoUrl = $originUrl
        Write-Host "Using existing remote origin as RepoUrl: $RepoUrl" -ForegroundColor Yellow
    } elseif ($env:NOFX_REPO_URL) {
        $RepoUrl = $env:NOFX_REPO_URL
        Write-Host "Using env NOFX_REPO_URL as RepoUrl: $RepoUrl" -ForegroundColor Yellow
    } else {
        $RepoUrl = "https://github.com/xiangqiangHe001/nofx.git"
        Write-Host "Using default RepoUrl: $RepoUrl" -ForegroundColor Yellow
    }
}

if (-not $Token -and $env:NOFX_GITHUB_PAT) {
    $Token = $env:NOFX_GITHUB_PAT
}

Write-Host "Starting upload to repo: $RepoUrl (branch: $Branch)" -ForegroundColor Cyan

# Proxy config and rollback
$prevHttpProxy = ''
$prevHttpsProxy = ''
$proxyWasApplied = $false

# Scope (local/global)
$scopeArgs = @()
$scopeName = 'local'
if ($GlobalProxy) { $scopeArgs += '--global'; $scopeName = 'global' }

# Pre-read global and local proxy, then choose by scope
$globalHttpGet = & git config --global --get http.proxy
if ($LASTEXITCODE -eq 0 -and $globalHttpGet) { $prevGlobalHttp = $globalHttpGet }
$globalHttpsGet = & git config --global --get https.proxy
if ($LASTEXITCODE -eq 0 -and $globalHttpsGet) { $prevGlobalHttps = $globalHttpsGet }

$localHttpGet = & git config --get http.proxy
if ($LASTEXITCODE -eq 0 -and $localHttpGet) { $prevLocalHttp = $localHttpGet }
$localHttpsGet = & git config --get https.proxy
if ($LASTEXITCODE -eq 0 -and $localHttpsGet) { $prevLocalHttps = $localHttpsGet }

$prevHttpProxy = $prevLocalHttp
if ($GlobalProxy -and $prevGlobalHttp) { $prevHttpProxy = $prevGlobalHttp }
$prevHttpsProxy = $prevLocalHttps
if ($GlobalProxy -and $prevGlobalHttps) { $prevHttpsProxy = $prevGlobalHttps }
Write-Verbose "Previous proxies selected ($scopeName): http=$prevHttpProxy https=$prevHttpsProxy"

if ($EnableProxy -or ($Proxy -and $Proxy.Length -gt 0)) {
    # 如果指定了 Proxy（无论是参数还是环境变量），且未显式禁止（此处简化为只要有Proxy就启用，除非未来加DisableProxy）
    # 但为了兼容旧逻辑，我们假设只要有Proxy值，就应该尝试启用，除非用户没传 EnableProxy？
    # 原逻辑是 $EnableProxy -and $Proxy。
    # 既然用户设置了默认环境变量，我们假设如果 Proxy 有值，就自动视为 EnableProxy = $true
    $EnableProxy = $true 
}

if ($EnableProxy -and $Proxy) {
    Write-Host "Apply git proxy ($scopeName): $Proxy" -ForegroundColor Yellow
    Write-Verbose "Applying proxies ($scopeName) via git config"
    & git config $scopeArgs http.proxy $Proxy
    & git config $scopeArgs https.proxy $Proxy
    $proxyWasApplied = $true
}

# Clear proxy and exit (safe path)
if ($ProxyClear) {
    Write-Host "Clearing git proxy ($scopeName)" -ForegroundColor Yellow
    Write-Verbose "Unsetting http.proxy and https.proxy ($scopeName)"
    & git config $scopeArgs --unset http.proxy
    & git config $scopeArgs --unset https.proxy
    Write-Host "Proxy cleared ($scopeName)" -ForegroundColor Green
    exit 0
}

# Check git availability
& git --version | Out-Null
if (-not $?) {
    Write-Error "git not found or not working. Please install and configure git."
    exit 1
}
Write-Verbose "Git is available"

# Init or confirm repository
if (-not (Test-Path (Join-Path $ProjectRoot '.git'))) {
    Write-Host "Initialize git repository" -ForegroundColor Yellow
    & git init | Out-Null
    Write-Verbose "Initialized repository at $ProjectRoot"
} else {
    Write-Verbose "Repository already initialized"
}

# Set local username/email to avoid commit failures
$userName = & git config user.name
if (-not $userName) { & git config user.name "auto-upload" }
$userEmail = & git config user.email
if (-not $userEmail) { & git config user.email "auto@local" }
Write-Verbose "Git user: $(& git config user.name) <$(& git config user.email)>"

# Configure remote origin
$remotes = & git remote
$hasOrigin = $false
if ($remotes) { $hasOrigin = ($remotes -contains 'origin') }
if ($hasOrigin) {
    Write-Host "Update remote origin: $RepoUrl" -ForegroundColor Yellow
    & git remote set-url origin $RepoUrl
}
if (-not $hasOrigin) {
    Write-Host "Add remote origin: $RepoUrl" -ForegroundColor Yellow
    & git remote add origin $RepoUrl
}
Write-Verbose "Remotes: $remotes; hasOrigin=$hasOrigin; origin=$(& git remote get-url origin)"

# Switch or create branch
& git checkout -B $Branch
if (-not $?) {
    Write-Error "Failed to switch branch: $Branch"
    exit 1
}
Write-Verbose "Checked out branch: $Branch"

# Stage and commit
$status = & git status --porcelain
if ($status) {
    & git add -A
    & git commit -m $CommitMessage
    Write-Verbose "Committed changes: $CommitMessage"
}
if (-not $status) {
    Write-Host "No changes to commit, pushing directly" -ForegroundColor Green
    Write-Verbose "Working tree clean"
}

# Push（优先使用Token，无需凭据管理器）
$pushArgs = @('push','-u','origin',$Branch)
if ($Force) { $pushArgs += '--force-with-lease' }
Write-Verbose "Push args: $pushArgs"

if ($Token) {
    try {
        $basic = [Convert]::ToBase64String([Text.Encoding]::ASCII.GetBytes("x-access-token:$Token"))
        $header = "Authorization: Basic $basic"
        & git -c credential.helper= -c "http.extraHeader=$header" $pushArgs
        if ($LASTEXITCODE -ne 0) {
            Write-Host "Push failed with token header. Retrying without extraHeader..." -ForegroundColor Yellow
            & git -c credential.helper= $pushArgs
            if ($LASTEXITCODE -ne 0) {
                Write-Host "Push failed (Token retry)." -ForegroundColor Red
                exit 1
            }
        }
    } catch {
        Write-Host "Push exception: $($_.Exception.Message)" -ForegroundColor Red
        & git -c credential.helper= $pushArgs
        if ($LASTEXITCODE -ne 0) { exit 1 }
    }
} else {
    & git $pushArgs
    if ($LASTEXITCODE -ne 0) {
        # 避免 'credential-manager-core' 缺失导致失败
        $helper = & git config --global --get credential.helper
        if ($helper -match 'manager-core|manager') {
            Write-Host "Detected credential.helper=$helper. Temporarily bypassing helper for this push." -ForegroundColor Yellow
        }
        & git -c credential.helper= $pushArgs
        if ($LASTEXITCODE -ne 0) {
            Write-Host "Push failed. Suggestion: Set env var NOFX_GITHUB_PAT or use -Token parameter (scope: repo)." -ForegroundColor Red
            Write-Host "Example: `$env:NOFX_GITHUB_PAT='ghp_xxx' or pass -Token 'ghp_xxx'" -ForegroundColor Cyan
            exit 1
        }
    }
}

# Restore proxy (safe)
if ($proxyWasApplied) {
    if ($prevHttpProxy) { & git config $scopeArgs http.proxy $prevHttpProxy } else { & git config $scopeArgs --unset http.proxy }
    if ($prevHttpsProxy) { & git config $scopeArgs https.proxy $prevHttpsProxy } else { & git config $scopeArgs --unset https.proxy }
    Write-Host "Proxy settings restored ($scopeName)" -ForegroundColor Yellow
    Write-Verbose "Restored proxies ($scopeName): http=$prevHttpProxy https=$prevHttpsProxy"
}

Write-Host "Upload complete: $RepoUrl ($Branch)" -ForegroundColor Green