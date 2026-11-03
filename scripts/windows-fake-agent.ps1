# A stand-in for Claude Code or Codex in scripts/windows-smoke.ps1: a background agent for a folder of plain videos.
# Retake runs it in the project's folder with the task's prompt on stdin, as it runs a real agent's headless command.
# It writes the next version (a copy of clip_v2.mp4) to $env:RETAKE_OUTPUT and its report to $env:RETAKE_REPORT. It
# never calls a provider.
$ErrorActionPreference = 'Stop'

$prompt = [Console]::In.ReadToEnd()
foreach ($v in @('RETAKE_CONTEXT', 'RETAKE_REPORT', 'RETAKE_OUTPUT')) {
    if (-not [Environment]::GetEnvironmentVariable($v)) { throw "the worker didn't set $v" }
}
$report = Split-Path -Leaf $env:RETAKE_REPORT
if ($prompt -notmatch [regex]::Escape($report)) { throw "the prompt on stdin doesn't name the report file $report" }
if (-not (Test-Path -LiteralPath $env:RETAKE_CONTEXT)) { throw "no task context at $env:RETAKE_CONTEXT" }
Write-Output "fake agent: in $((Get-Location).Path), writing $env:RETAKE_OUTPUT"

Copy-Item -LiteralPath (Join-Path (Get-Location).Path 'clip_v2.mp4') -Destination $env:RETAKE_OUTPUT -Force
# UTF-8 without a byte-order mark: Retake reads the report as JSON.
[System.IO.File]::WriteAllText($env:RETAKE_REPORT, '{"state":"completed","message":"Made v2 again as v3."}')
Write-Output 'fake agent: done'
