<#
.SYNOPSIS
Retake on Windows end to end, without a person: retake start on a scratch videos folder, the page and a poster, then
a comment sent to a fake agent (PowerShell) that makes the next version in the background.

.DESCRIPTION
Uses scripts\build-windows.ps1's dist\windows (retake.exe with the page, ffmpeg.exe, ffprobe.exe, retake-bundle.json).
Everything it makes is under -Dir, with a config of its own (XDG_CONFIG_HOME and friends point there), and it stops
the server it started. It never runs a real agent. Exit code 0 when every check passed; else 1 with the reason and
the server's log.

  pwsh scripts/windows-smoke.ps1
  pwsh scripts/windows-smoke.ps1 -Bin dist\windows -Port 9271 -Keep

.PARAMETER Bin
The folder with retake.exe and ffmpeg.exe. Default: <repo>\dist\windows.

.PARAMETER Dir
The scratch folder. Default: $env:RUNNER_TEMP (CI), else $env:TEMP, + \retake-smoke. Emptied first.

.PARAMETER Port
The port retake start listens on (127.0.0.1).

.PARAMETER Keep
Keep the scratch folder after a passing run.
#>
[CmdletBinding()]
param(
    [string]$Bin = '',
    [string]$Dir = '',
    [int]$Port = 9271,
    [switch]$Keep
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$Root = Split-Path -Parent $PSScriptRoot
if (-not $Bin) { $Bin = Join-Path $Root 'dist\windows' }
if (-not [System.IO.Path]::IsPathRooted($Bin)) { $Bin = Join-Path (Get-Location).Path $Bin }
$Bin = [System.IO.Path]::GetFullPath($Bin)
if (-not $Dir) {
    $t = $env:RUNNER_TEMP
    if (-not $t) { $t = [System.IO.Path]::GetTempPath() }
    $Dir = Join-Path $t 'retake-smoke'
}
if (-not [System.IO.Path]::IsPathRooted($Dir)) { $Dir = Join-Path (Get-Location).Path $Dir }
$Dir = [System.IO.Path]::GetFullPath($Dir)
$retake = Join-Path $Bin 'retake.exe'
$ffmpeg = Join-Path $Bin 'ffmpeg.exe'
$agent = Join-Path $PSScriptRoot 'windows-fake-agent.ps1'
$base = "http://127.0.0.1:$Port/"
$server = $null
$failed = $false

function Say([string]$Text) { Write-Host "smoke: $Text" }

# HttpGet is a GET of a URL relative to the server: the response, or a throw that says what went wrong.
function HttpGet([string]$Path, [string]$OutFile = '') {
    $req = @{ UseBasicParsing = $true; Uri = "$base$Path"; TimeoutSec = 30 }
    if ($OutFile) { $req.OutFile = $OutFile; $req.PassThru = $true }
    return Invoke-WebRequest @req
}

function GetJson([string]$Path) { return (HttpGet $Path).Content | ConvertFrom-Json }

function PostJson([string]$Path, [string]$Body) {
    return Invoke-WebRequest -UseBasicParsing -Method Post -Uri "$base$Path" -ContentType 'application/json' -Body $Body -TimeoutSec 30
}

# Until polls Check (a script block) every 200 ms until it returns something, for at most Seconds; a throw from it
# counts as not yet. It returns what Check returned, or throws "timed out: What" with the last error.
function Until([string]$What, [int]$Seconds, [scriptblock]$Check) {
    $deadline = (Get-Date).AddSeconds($Seconds)
    $last = ''
    while ((Get-Date) -lt $deadline) {
        if ($server -and $server.HasExited) { throw "retake exited (code $($server.ExitCode)) while waiting for $What" }
        try {
            $r = & $Check
            if ($r) { return $r }
        } catch {
            $last = "$_"
        }
        Start-Sleep -Milliseconds 200
    }
    throw "timed out after $Seconds s waiting for $What $(if ($last) { "(last error: $last)" })"
}

function Clip([string]$Path, [double]$Seconds) {
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $Path) | Out-Null
    $a = @('-nostdin', '-v', 'error', '-y', '-f', 'lavfi', '-i', "testsrc2=s=64x36:r=10:d=$Seconds",
        '-f', 'lavfi', '-i', "sine=f=440:sample_rate=48000:d=$Seconds",
        '-c:v', 'libx264', '-preset', 'ultrafast', '-pix_fmt', 'yuv420p', '-c:a', 'aac', '-shortest',
        '-movflags', '+faststart', $Path)
    & $ffmpeg @a
    if ($LASTEXITCODE -ne 0) { throw "ffmpeg couldn't make $Path (exit code $LASTEXITCODE)" }
}

try {
    foreach ($f in @($retake, $ffmpeg, $agent)) {
        if (-not (Test-Path -LiteralPath $f)) { throw "missing $f (run scripts\build-windows.ps1 first)" }
    }
    foreach ($p in @($Dir, $agent)) {
        if ($p.Contains("'")) { throw "the path $p has a ' in it, which the TOML config below can't hold" }
    }
    if (Test-Path -LiteralPath $Dir) { Remove-Item -LiteralPath $Dir -Recurse -Force }
    $videos = Join-Path $Dir 'videos'
    $promo = Join-Path $videos 'promo'

    # 1. A folder of plain videos: promo has v1 and v2. Made with the ffmpeg that ships with Retake.
    Say "clips in $promo"
    Clip (Join-Path $promo 'clip_v1.mp4') 2
    Clip (Join-Path $promo 'clip_v2.mp4') 3

    # 2. A config of its own: the folder, no browser, and only the fake agent (find_agents = false: a real Claude
    #    Code or Codex on this machine is never run). Literal TOML strings ('...') keep the backslashes.
    foreach ($v in @('XDG_CONFIG_HOME', 'XDG_STATE_HOME', 'XDG_DATA_HOME', 'XDG_CACHE_HOME')) {
        $d = Join-Path $Dir ($v.Substring(4).ToLowerInvariant())
        New-Item -ItemType Directory -Force -Path $d | Out-Null
        Set-Item -Path "Env:$v" -Value $d
    }
    $cfgDir = Join-Path $env:XDG_CONFIG_HOME 'retake'
    New-Item -ItemType Directory -Force -Path $cfgDir | Out-Null
    $config = @"
name = "Smoke Test"
open_browser = false
idle_exit_minutes = 0
find_agents = false
default_agent = "fake"
folder = '$videos'

[[agents]]
id = "fake"
name = "Fake agent"
command = ['powershell', '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-File', '$agent']
"@
    [System.IO.File]::WriteAllText((Join-Path $cfgDir 'config.toml'), $config)

    # 3. retake start, as the Start menu entry runs it, but with no browser and a fixed port.
    $out = Join-Path $Dir 'retake.out.log'
    $err = Join-Path $Dir 'retake.err.log'
    Say "$retake start --no-open --port $Port"
    $server = Start-Process -FilePath $retake -ArgumentList @('start', '--no-open', '--port', "$Port") -WorkingDirectory $Dir `
        -RedirectStandardOutput $out -RedirectStandardError $err -PassThru -NoNewWindow
    Until 'api/ping' 30 { (HttpGet 'api/ping').StatusCode -eq 200 } | Out-Null
    Say "up at $base (pid $($server.Id))"

    # 4. The page and the home's API.
    $page = HttpGet ''
    if ($page.StatusCode -ne 200 -or $page.Content -notmatch '(?i)<html') { throw "the page at / isn't the review page: $($page.StatusCode)" }
    Say 'the page answers'
    $video = Until 'promo at v2 on the home' 60 {
        $h = GetJson 'api/home'
        $h.projects | Where-Object { $_.name -eq 'promo' -and $_.version -eq 'v2' } | Select-Object -First 1
    }
    if (-not $video.thumb) { throw "promo has no poster URL on the home: $($video | ConvertTo-Json -Compress)" }

    # 5. Its poster: a JPEG of v2's first frame, made with the bundled ffmpeg.
    $jpg = Join-Path $Dir 'poster.jpg'
    $r = Until 'the poster' 60 { $x = HttpGet $video.thumb $jpg; if ($x.StatusCode -eq 200) { $x } }
    $bytes = [System.IO.File]::ReadAllBytes($jpg)
    if ("$($r.Headers['Content-Type'])" -notmatch 'image/jpeg' -or $bytes.Length -lt 100 -or $bytes[0] -ne 0xFF -or $bytes[1] -ne 0xD8) {
        throw "the poster ($($video.thumb)) isn't a JPEG: $($r.Headers['Content-Type']), $($bytes.Length) bytes"
    }
    Say "poster: $($video.thumb), $($bytes.Length) bytes"

    # 6. The video's worker, in this process, with the fake agent.
    $auto = Until "promo's worker with the fake agent" 60 {
        $a = GetJson 'p/promo/api/automation'
        if ($a.blocked) { throw "blocked: $($a.blocked)" }
        if ($a.local -and @($a.workers).Count -ge 1 -and $a.config.agent -eq 'fake') { $a }
    }
    Say "worker: $(@($auto.workers)[0].name)"

    # 7. A typed comment on v2, then Send: the agent runs in the background and its video becomes v3.
    $note = PostJson 'p/promo/api/notes' '{"version": "v2", "t": 0.5, "text": "Once more, from the Windows smoke test"}'
    if ($note.StatusCode -ne 201) { throw "the typed comment: $($note.StatusCode) $($note.Content)" }
    $id = ($note.Content | ConvertFrom-Json).id
    Say "comment #$id on v2"
    $send = PostJson 'p/promo/api/automation/tasks' "{`"notes`": [$id]}"
    if ($send.StatusCode -ne 200) { throw "send: $($send.StatusCode) $($send.Content)" }
    $task = Until 'the task to end' 120 {
        $t = @((GetJson 'p/promo/api/automation').tasks) | Select-Object -First 1
        if ($t -and $t.state -in @('completed', 'paused', 'question', 'cancelled')) { $t }
    }
    if ($task.state -ne 'completed' -or $task.result -ne 'v3') {
        throw "the task ended $($task.state) (result '$($task.result)'): $($task.error)$($task.question)"
    }
    $v3 = Join-Path $promo 'clip_v3.mp4'
    if (-not (Test-Path -LiteralPath $v3)) { throw "the task completed, but there's no $v3" }
    Until 'v3 on the home' 60 {
        (GetJson 'api/home').projects | Where-Object { $_.name -eq 'promo' -and $_.version -eq 'v3' }
    } | Out-Null
    Say 'the fake agent made v3 in the background'
    Say 'passed'
} catch {
    $failed = $true
    Write-Host "smoke: FAILED: $_"
    foreach ($log in @((Join-Path $Dir 'retake.out.log'), (Join-Path $Dir 'retake.err.log'))) {
        if (Test-Path -LiteralPath $log) {
            Write-Host "----- $log"
            Get-Content -LiteralPath $log -Tail 200 | Write-Host
        }
    }
    $logs = Get-ChildItem -LiteralPath $Dir -Recurse -File -Filter 'agent-*.log' -ErrorAction SilentlyContinue
    foreach ($l in $logs) {
        Write-Host "----- $($l.FullName)"
        Get-Content -LiteralPath $l.FullName -Tail 50 | Write-Host
    }
} finally {
    if ($server -and -not $server.HasExited) {
        Stop-Process -Id $server.Id -Force -ErrorAction SilentlyContinue
        $server.WaitForExit(10000) | Out-Null
    }
    if (-not $failed -and -not $Keep) { Remove-Item -LiteralPath $Dir -Recurse -Force -ErrorAction SilentlyContinue }
}
if ($failed) { exit 1 }
exit 0
