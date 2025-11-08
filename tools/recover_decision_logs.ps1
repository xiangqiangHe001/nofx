param(
  [string]$RecycleDrive = 'D',
  [string]$ProjectPath = 'D:\TRAE\projerct',
  [switch]$Restore,
  [switch]$VerboseLog
)

function Get-UserSid {
  [System.Security.Principal.WindowsIdentity]::GetCurrent().User.Value
}

function Get-RecycleBinSidPath([string]$drive, [string]$sid) {
  $root = "${drive}:\"
  $bin = Join-Path -Path $root -ChildPath '$Recycle.Bin'
  Join-Path -Path $bin -ChildPath $sid
}

function Parse-IFileOriginalPath([string]$infoFilePath) {
  try {
    $bytes = [System.IO.File]::ReadAllBytes($infoFilePath)
    if ($bytes.Length -lt 24) { return $null }
    # Windows $I file format: 8 bytes header, 8 bytes file size, 8 bytes deletion time, then UTF-16LE original path
    $pathBytes = $bytes[24..($bytes.Length-1)]
    $origPath = [System.Text.Encoding]::Unicode.GetString($pathBytes).Trim([char]0)
    return $origPath
  } catch {
    return $null
  }
}

function Restore-FromRecyclePair([string]$infoFilePath, [string]$origPath) {
  $base = [System.IO.Path]::GetFileNameWithoutExtension($infoFilePath) # $Ixxxxxxxxxx
  $rFile = Join-Path -Path ([System.IO.Path]::GetDirectoryName($infoFilePath)) -ChildPath ($base -replace '^\$I', '$R')
  if (!(Test-Path $rFile)) { Write-Warning "对应的 $R 文件不存在: $rFile"; return $false }
  $targetDir = [System.IO.Path]::GetDirectoryName($origPath)
  if (!(Test-Path $targetDir)) { New-Item -ItemType Directory -Force -Path $targetDir | Out-Null }
  try {
    Copy-Item -LiteralPath $rFile -Destination $origPath -Force
    return $true
  } catch {
    Write-Warning "恢复失败: $origPath -> $rFile | $($_.Exception.Message)"
    return $false
  }
}

$sid = Get-UserSid
$sidPath = Get-RecycleBinSidPath -drive $RecycleDrive -sid $sid
if (!(Test-Path $sidPath)) {
  Write-Error "找不到当前用户回收站路径: $sidPath"
  exit 1
}

Write-Host "回收站路径: $sidPath"
Write-Host "项目路径过滤: $ProjectPath"

# 仅枚举 $I* 文件，避免深度递归导致卡顿
$iFiles = Get-ChildItem -LiteralPath $sidPath -Filter '$I*' -File -ErrorAction SilentlyContinue
if ($iFiles.Count -eq 0) {
  Write-Host "未发现 $I 文件，可能无可恢复记录。"
  exit 0
}

$candidates = @()
foreach ($i in $iFiles) {
  $orig = Parse-IFileOriginalPath -infoFilePath $i.FullName
  if ([string]::IsNullOrWhiteSpace($orig)) { continue }
  # 仅匹配项目路径并包含 decision 关键词
  if ($orig -like "$ProjectPath*" -and ($orig -like '*decision_logs*' -or $orig -like '*decision_*.json*')) {
    $candidates += [pscustomobject]@{ Info=$i.FullName; Original=$orig }
  }
}

if ($candidates.Count -eq 0) {
  Write-Host "未在回收站中发现与 decision 日志相关的记录。"
  exit 0
}

Write-Host "发现候选项 $($candidates.Count) 条："
foreach ($c in $candidates) {
  Write-Host ("- 原路径: {0}" -f $c.Original)
  if ($VerboseLog) { Write-Host ("  `$I: {0}" -f $c.Info) }
}

if ($Restore) {
  Write-Host "开始恢复..."
  $ok = 0; $fail = 0
  foreach ($c in $candidates) {
    if (Restore-FromRecyclePair -infoFilePath $c.Info -origPath $c.Original) { $ok++ } else { $fail++ }
  }
  Write-Host ("恢复完成：成功 {0}，失败 {1}" -f $ok, $fail)
} else {
  Write-Host 'Preview mode: no restore executed. Use -Restore to recover.'
}