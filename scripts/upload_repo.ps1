[CmdletBinding()]
param(
    [Parameter(Mandatory=$true)] [string]$RepoUrl,
    [string]$Branch = "main",
    [string]$CommitMessage = "chore: whitelist coins & upload (auto)",
    [switch]$Force,
    [string]$Proxy,
    [switch]$EnableProxy,
    [switch]$GlobalProxy,
    [switch]$ProxyClear
)

Write-Host "Starting upload to repo: $RepoUrl (branch: $Branch)" -ForegroundColor Cyan

# Project root (script is under scripts/)
$ProjectRoot = Split-Path $PSScriptRoot -Parent
Set-Location $ProjectRoot
Write-Verbose "Project root: $ProjectRoot"

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

if ($EnableProxy -and $Proxy) {
    Write-Host "Apply git proxy ($scopeName): $Proxy" -ForegroundColor Yellow
    Write-Verbose "Applying proxies ($scopeName) via git config"
    & git config @scopeArgs http.proxy $Proxy
    & git config @scopeArgs https.proxy $Proxy
    $proxyWasApplied = $true
}

# Clear proxy and exit (safe path)
if ($ProxyClear) {
    Write-Host "Clearing git proxy ($scopeName)" -ForegroundColor Yellow
    Write-Verbose "Unsetting http.proxy and https.proxy ($scopeName)"
    & git config @scopeArgs --unset http.proxy
    & git config @scopeArgs --unset https.proxy
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

# Push
$pushArgs = @('push','-u','origin',$Branch)
if ($Force) { $pushArgs += '--force-with-lease' }
Write-Verbose "Push args: $pushArgs"
& git @pushArgs

# Restore proxy (safe)
if ($proxyWasApplied) {
    if ($prevHttpProxy) { & git config @scopeArgs http.proxy $prevHttpProxy } else { & git config @scopeArgs --unset http.proxy }
    if ($prevHttpsProxy) { & git config @scopeArgs https.proxy $prevHttpsProxy } else { & git config @scopeArgs --unset https.proxy }
    Write-Host "Proxy settings restored ($scopeName)" -ForegroundColor Yellow
    Write-Verbose "Restored proxies ($scopeName): http=$prevHttpProxy https=$prevHttpsProxy"
}

Write-Host "Upload complete: $RepoUrl ($Branch)" -ForegroundColor Green