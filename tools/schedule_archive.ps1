param(
  [string]$TimeStamp = (Get-Date -Format 'yyyyMMdd'),
  [string]$Root,
  [string]$Out
)

# 计算项目根目录与输出目录
$RepoRoot = if ($Root) { $Root } else { (Split-Path -Parent $PSScriptRoot) }
if (-not $Out) { $Out = (Join-Path -Path $RepoRoot -ChildPath 'archives') }

Write-Host ("Archive date: {0}" -f $TimeStamp)
Write-Host ("Project root: {0}" -f $RepoRoot)
Write-Host ("Output dir: {0}" -f $Out)

# 组装 go run 参数
$GoFile = (Join-Path -Path $PSScriptRoot -ChildPath 'archive_decision_logs.go')
$Args = @('run', $GoFile, '-root', $RepoRoot, '-out', $Out, '-date', $TimeStamp)
Write-Host ("Running: go {0}" -f ($Args -join ' '))

try {
  $p = Start-Process -FilePath 'go' -ArgumentList $Args -NoNewWindow -Wait -PassThru
  if ($p.ExitCode -ne 0) {
    Write-Error ("Archive task failed, exit code: {0}" -f $p.ExitCode)
    exit $p.ExitCode
  }
  Write-Host 'Archive task finished.'
} catch {
  Write-Error ("执行失败: {0}" -f $_.Exception.Message)
  exit 1
}