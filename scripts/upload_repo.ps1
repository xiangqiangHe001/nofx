param(
    [Parameter(Mandatory=$true)] [string]$RepoUrl,
    [string]$Branch = "main",
    [string]$CommitMessage = "chore: whitelist coins & upload (auto)",
    [switch]$Force
)

Write-Host "Starting upload to repo: $RepoUrl (branch: $Branch)" -ForegroundColor Cyan

# 项目根目录（脚本位于 scripts/ 下）
$ProjectRoot = Split-Path $PSScriptRoot -Parent
Set-Location $ProjectRoot

function Exec($cmd) {
    Write-Host "=> $cmd" -ForegroundColor DarkGray
    $output = Invoke-Expression $cmd
    return $output
}

# 检查 git 可用
$gitVer = Exec 'git --version'
if (-not $?) {
    Write-Error "git not found or not working. Please install and configure git."
    exit 1
}

# 初始化或确认仓库
if (-not (Test-Path (Join-Path $ProjectRoot '.git'))) {
    Write-Host "Initialize git repository" -ForegroundColor Yellow
    Exec 'git init'
}

# 设置本地用户名/邮箱（避免提交失败）
$userName = Exec 'git config user.name'
if (-not $userName) { Exec 'git config user.name "auto-upload"' }
$userEmail = Exec 'git config user.email'
if (-not $userEmail) { Exec 'git config user.email "auto@local"' }

# 设置远程 origin
$hasOrigin = $false
$remoteUrl = Exec 'git remote get-url origin 2>$null'
if ($?) { $hasOrigin = $true } else { $hasOrigin = $false }

if (-not $hasOrigin) {
    Write-Host "Add remote origin: $RepoUrl" -ForegroundColor Yellow
    Exec ('git remote add origin "' + $RepoUrl + '"')
} else {
    Write-Host "Update remote origin: $RepoUrl" -ForegroundColor Yellow
    Exec ('git remote set-url origin "' + $RepoUrl + '"')
}

# 切换或创建分支
$checkout = Exec "git checkout -B $Branch"
if (-not $?) {
    Write-Error "Failed to switch branch: $Branch"
    exit 1
}

# 暂存并提交
$status = Exec 'git status --porcelain'
if (-not $status) {
    Write-Host "No changes to commit, pushing directly" -ForegroundColor Green
} else {
    Exec 'git add -A'
    Exec ('git commit -m "' + $CommitMessage + '"')
}

# 推送
if ($Force) {
    Exec "git push -u origin $Branch --force-with-lease"
}
if (-not $Force) {
    Exec "git push -u origin $Branch"
}

Write-Host "Upload complete: $RepoUrl ($Branch)" -ForegroundColor Green