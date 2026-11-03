<#
.SYNOPSIS
Builds Retake for Windows x64 into one folder and one zip, with the tools that ship with it (docs/install-plan.md).

.DESCRIPTION
Run it on Windows x64 (CI's windows-latest; the release workflow calls it there). It needs git, Go and node (the
versions in mise.toml) and, unless -SkipEditor, Rust with the MSVC toolchain. Downloads are pinned by SHA-256 and kept
in a cache folder, so a second run downloads nothing. Nothing downloaded is run here.

What it makes, in -OutDir (default: <repo>\dist):

  windows\retake.exe            the server, the page (embedded, -tags webdist) and the worker
  windows\retakew.exe           the same program's window-less launcher (cmd\retakew): the Start menu entry and the
                                login item run it, so no console window opens
  windows\retake-edit.exe       the timeline renderer (editor\, cargo, MSVC)
  windows\ffmpeg.exe, ffprobe.exe   a static GPL build with libx264 and zimg (gyan.dev's "essentials", via GitHub)
  windows\herdr.exe, windows\conpty\   herdr's Windows build, with the ConPTY files it loads from beside it
  windows\retake-bundle.json    what's inside (bundle 1, version, tools): retake puts its own folder first on PATH
  windows\licenses\             each tool's license text and ffmpeg's source offer
  windows\README.md, CHANGELOG.md, LICENSE, THIRD_PARTY_NOTICES.md
  retake_<version>_windows_amd64.zip    that folder's contents at the zip's top, named like goreleaser's archives
  checksums-windows.txt         "<sha256>  <zip name>", the line to add to the release's checksums.txt

Examples:
  pwsh scripts/build-windows.ps1                       # everything; the version from git tags (scripts/version.sh)
  pwsh scripts/build-windows.ps1 -Version 0.4.0
  pwsh scripts/build-windows.ps1 -SkipEditor -SkipFfmpeg   # just retake.exe and retakew.exe (quick)

.PARAMETER Version
The version, without a leading v. Default: worked out from the newest vX.Y.Z git tag, as scripts/version.sh does.

.PARAMETER OutDir
Where windows\, the zip and checksums-windows.txt go. Default: <repo>\dist.

.PARAMETER SkipWeb
Use web\dist as it is (the page must have been built already) instead of npm ci and npm run build.

.PARAMETER SkipEditor
Leave out retake-edit.exe (no cargo build).

.PARAMETER SkipFfmpeg
Leave out ffmpeg.exe, ffprobe.exe and herdr.exe (no downloads).
#>
[CmdletBinding()]
param(
    [string]$Version = '',
    [string]$OutDir = '',
    [switch]$SkipWeb,
    [switch]$SkipEditor,
    [switch]$SkipFfmpeg
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue' # Windows PowerShell's progress bar makes downloads many times slower
Set-StrictMode -Version 2.0
# Windows PowerShell 5.1 may still offer only TLS 1.0 to GitHub.
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

# The product's name isn't final: these are the places this script names it.
$Name = 'retake'
$EditorName = 'retake-edit'

# ffmpeg: Gyan Doshi's static GPL "essentials" build (libx264, zimg, among others; its README lists them), kept on
# GitHub as a versioned release. The same pin as scripts/bundle-deps.sh's Windows ffmpeg.
$FfmpegVersion = '9.0.2'
$FfmpegUrl = "https://github.com/GyanD/codexffmpeg/releases/download/$FfmpegVersion/ffmpeg-$FfmpegVersion-essentials_build.zip"
$FfmpegSha = '60f467265b1e312373dbcd92200c2618a74850f98d3d078e94296bb3fa2047ba'
$FfmpegSource = "https://ffmpeg.org/releases/ffmpeg-$FfmpegVersion.tar.xz"

# herdr: its Windows build (herdr.exe and the ConPTY files it loads from the conpty\ folder beside it, kept whole,
# arm64 OpenConsole.exe too, as herdr-conpty.json lists it). One herdr version on every platform: the version
# scripts/bundle-deps.sh pins.
$HerdrVersion = '0.8.2'
$HerdrUrl = "https://github.com/herdrdev/herdr/releases/download/v$HerdrVersion/herdr-windows-x86_64.zip"
$HerdrSha = '0ab3d0fe1434d55757997542b978c771d642987bb15a7130f4160f0db38821d5'
$HerdrLicenseUrl = "https://raw.githubusercontent.com/herdrdev/herdr/v$HerdrVersion/LICENSE"
$HerdrLicenseSha = 'c71d239df91726fc519c6eb72d318ec65820627232b2f796219e87dcf35d0ab4'

$Root = Split-Path -Parent $PSScriptRoot
if (-not $OutDir) { $OutDir = Join-Path $Root 'dist' }
$OutDir = [System.IO.Path]::GetFullPath($OutDir)
$Stage = Join-Path $OutDir 'windows'

function Say([string]$Text) { Write-Host "build-windows: $Text" }

# Exec runs a program and stops the build when it fails. Its stderr isn't redirected, so Windows PowerShell doesn't
# turn the progress lines cargo and npm print there into errors.
function Exec {
    param([Parameter(Mandatory = $true)][string]$File, [string[]]$Arguments = @(), [string]$Dir = '')
    Write-Host "> $File $($Arguments -join ' ')"
    if ($Dir) { Push-Location -LiteralPath $Dir }
    try {
        & $File @Arguments
        if ($LASTEXITCODE -ne 0) { throw "$File $($Arguments -join ' ') failed (exit code $LASTEXITCODE)" }
    } finally {
        if ($Dir) { Pop-Location }
    }
}

# Output runs a program for its output (trimmed), "" when it fails; its stderr is dropped.
function Output {
    param([Parameter(Mandatory = $true)][string]$File, [string[]]$Arguments = @())
    $old = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $out = & $File @Arguments 2>$null
        if ($LASTEXITCODE -ne 0) { return '' }
        return (($out | Out-String).Trim())
    } catch {
        return ''
    } finally {
        $ErrorActionPreference = $old
    }
}

# The version as scripts/version.sh prints it: X.Y.Z on a tag, X.Y.(Z+1)-dev.N+g<sha> after one, 0.0.0-dev+g<sha>
# before any, ".dirty" with uncommitted changes, 0.0.0-dev outside a git checkout.
function Get-Version {
    if (-not (Output git @('-C', $Root, 'rev-parse', '--git-dir'))) { return '0.0.0-dev' }
    $sha = Output git @('-C', $Root, 'rev-parse', '--short=7', 'HEAD')
    $dirty = [bool](Output git @('-C', $Root, 'status', '--porcelain', '--untracked-files=no'))
    $tag = Output git @('-C', $Root, 'describe', '--tags', '--abbrev=0', '--match', 'v[0-9]*.[0-9]*.[0-9]*')
    if (-not $tag) {
        if ($dirty) { return "0.0.0-dev+g$sha.dirty" }
        return "0.0.0-dev+g$sha"
    }
    $base = $tag.Substring(1)
    $n = [int](Output git @('-C', $Root, 'rev-list', '--count', "$tag..HEAD"))
    if ($n -eq 0) {
        if ($dirty) { return "$base+dirty" }
        return $base
    }
    $core = ($base -split '-', 2)[0]
    $parts = $core -split '\.'
    if ($parts.Count -ne 3) { throw "tag $tag isn't vX.Y.Z" }
    $patch = [int]$parts[2]
    if ($core -eq $base) { $patch++ } # after an rc tag: the same X.Y.Z
    $v = "$($parts[0]).$($parts[1]).$patch-dev.$n+g$sha"
    if ($dirty) { $v += '.dirty' }
    return $v
}

function Get-Sha256([string]$Path) { (Get-FileHash -Algorithm SHA256 -LiteralPath $Path).Hash.ToLowerInvariant() }

# Fetch returns the file at Url from the cache (downloaded once), after checking its SHA-256 every time.
function Fetch([string]$Url, [string]$FileName, [string]$Sha) {
    $cache = $env:RETAKE_BUNDLE_CACHE
    if (-not $cache) {
        if ($env:LOCALAPPDATA) { $cache = Join-Path $env:LOCALAPPDATA 'retake-bundle' }
        else { $cache = Join-Path ([System.IO.Path]::GetTempPath()) 'retake-bundle' }
    }
    New-Item -ItemType Directory -Force -Path $cache | Out-Null
    $file = Join-Path $cache $FileName
    if (-not (Test-Path -LiteralPath $file)) {
        Say "downloading $Url"
        $part = "$file.part"
        Invoke-WebRequest -UseBasicParsing -Uri $Url -OutFile $part
        Move-Item -LiteralPath $part -Destination $file -Force
    }
    $got = Get-Sha256 $file
    if ($got -ne $Sha) {
        throw "$FileName`: SHA-256 is $got, want $Sha (refusing it; delete $file to download it again)"
    }
    return $file
}

# Unzip writes the entries of Zip whose names (the archive's own top folder taken off when TopFolder is set) are
# keys of Map to the file each maps to. A missing entry stops the build.
function Unzip([string]$Zip, [hashtable]$Map, [switch]$TopFolder) {
    Add-Type -AssemblyName System.IO.Compression, System.IO.Compression.FileSystem
    $found = @{}
    $z = [System.IO.Compression.ZipFile]::OpenRead($Zip)
    try {
        foreach ($e in $z.Entries) {
            $n = $e.FullName -replace '\\', '/'
            if ($TopFolder) { $n = $n -replace '^[^/]+/', '' }
            if ($Map.ContainsKey($n)) {
                $dest = $Map[$n]
                New-Item -ItemType Directory -Force -Path (Split-Path -Parent $dest) | Out-Null
                [System.IO.Compression.ZipFileExtensions]::ExtractToFile($e, $dest, $true)
                $found[$n] = $true
            }
        }
    } finally {
        $z.Dispose()
    }
    foreach ($k in $Map.Keys) {
        if (-not $found.ContainsKey($k)) { throw "$(Split-Path -Leaf $Zip) has no $k" }
    }
}

function Write-Text([string]$Path, [string]$Text) {
    # UTF-8 without a byte-order mark, LF line ends: what Go's JSON reader and Unix tools expect.
    [System.IO.File]::WriteAllText($Path, ($Text -replace "`r`n", "`n"))
}

if (-not $Version) { $Version = Get-Version }
$Version = $Version.TrimStart('v')
if ($Version -notmatch '^[0-9]+\.[0-9]+\.[0-9]+[0-9A-Za-z.+-]*$') { throw "-Version wants X.Y.Z, not $Version" }
Say "$Name $Version for windows/amd64 into $Stage"

if (Test-Path -LiteralPath $Stage) { Remove-Item -LiteralPath $Stage -Recurse -Force }
New-Item -ItemType Directory -Force -Path (Join-Path $Stage 'licenses') | Out-Null

# 1. The page, built into web\dist (the binary embeds it).
if (-not $SkipWeb) {
    Exec npm @('ci') (Join-Path $Root 'web')
    Exec npm @('run', 'build') (Join-Path $Root 'web')
} elseif (-not (Test-Path -LiteralPath (Join-Path $Root 'web\dist\index.html'))) {
    throw '-SkipWeb, but web\dist has no page: build it first (npm ci; npm run build in web\)'
}

# 2. retake.exe and retakew.exe, as goreleaser builds retake (pure Go, the page embedded, the version linked in).
$module = Output go @('-C', $Root, 'list', '-m')
if (-not $module) { throw "go list -m failed in $Root (is Go installed?)" }
if (-not (Test-Path -LiteralPath (Join-Path $Root 'cmd\retakew'))) { throw 'cmd\retakew is missing: the launcher the Start menu runs' }
$saved = @{ CGO_ENABLED = $env:CGO_ENABLED; GOOS = $env:GOOS; GOARCH = $env:GOARCH }
$env:CGO_ENABLED = '0'
$env:GOOS = 'windows'
$env:GOARCH = 'amd64'
try {
    $ld = "-s -w -X $module/internal/cli.version=$Version"
    Exec go @('build', '-tags', 'webdist', '-trimpath', '-ldflags', $ld, '-o', (Join-Path $Stage "$Name.exe"), './cmd/retake') $Root
    Exec go @('build', '-trimpath', '-ldflags', "$ld -H=windowsgui", '-o', (Join-Path $Stage "${Name}w.exe"), './cmd/retakew') $Root
} finally {
    foreach ($k in $saved.Keys) { [Environment]::SetEnvironmentVariable($k, $saved[$k]) } # $null: unset again
}

# 3. retake-edit.exe: it links prebuilt Skia, so it's built here, natively (MSVC), not cross-compiled.
if (-not $SkipEditor) {
    Exec cargo @('build', '--release', '--locked', '-p', $EditorName, '--manifest-path', (Join-Path $Root 'editor\Cargo.toml'))
    Copy-Item -LiteralPath (Join-Path $Root "editor\target\release\$EditorName.exe") -Destination $Stage
}

# 4. ffmpeg, ffprobe and herdr, pinned.
$tools = [ordered]@{}
if (-not $SkipFfmpeg) {
    $zip = Fetch $FfmpegUrl "ffmpeg-$FfmpegVersion-essentials_build.zip" $FfmpegSha
    Unzip $zip @{
        'bin/ffmpeg.exe'  = (Join-Path $Stage 'ffmpeg.exe')
        'bin/ffprobe.exe' = (Join-Path $Stage 'ffprobe.exe')
        'LICENSE'         = (Join-Path $Stage 'licenses\ffmpeg-GPLv3.txt')
        'README.txt'      = (Join-Path $Stage 'licenses\ffmpeg-build-readme.txt')
    } -TopFolder
    Write-Text (Join-Path $Stage 'licenses\ffmpeg-SOURCE.txt') (
        "ffmpeg $FfmpegVersion, a static GPL build by Gyan Doshi (https://www.gyan.dev/ffmpeg/builds/, kept at $FfmpegUrl).`n" +
        "Its source is FFmpeg ${FfmpegVersion}: $FfmpegSource`n")
    $tools['ffmpeg'] = [ordered]@{ version = $FfmpegVersion; license = 'GPL-3.0-or-later'; source = $FfmpegSource }

    $zip = Fetch $HerdrUrl "herdr-$HerdrVersion-windows-x86_64.zip" $HerdrSha
    Unzip $zip @{
        'herdr.exe'                                                   = (Join-Path $Stage 'herdr.exe')
        'conpty/conpty.dll'                                           = (Join-Path $Stage 'conpty\conpty.dll')
        'conpty/herdr-conpty.json'                                    = (Join-Path $Stage 'conpty\herdr-conpty.json')
        'conpty/x64/OpenConsole.exe'                                  = (Join-Path $Stage 'conpty\x64\OpenConsole.exe')
        'conpty/arm64/OpenConsole.exe'                                = (Join-Path $Stage 'conpty\arm64\OpenConsole.exe')
        'THIRD-PARTY-NOTICES/Microsoft.Windows.Console.ConPTY-LICENSE.txt' = (Join-Path $Stage 'licenses\herdr-conpty-LICENSE.txt')
        'THIRD-PARTY-NOTICES/Microsoft.Windows.Console.ConPTY-NOTICE.md'   = (Join-Path $Stage 'licenses\herdr-conpty-NOTICE.md')
    }
    Copy-Item -LiteralPath (Fetch $HerdrLicenseUrl "herdr-LICENSE-v$HerdrVersion" $HerdrLicenseSha) -Destination (Join-Path $Stage 'licenses\herdr-LICENSE.txt')
    $tools['herdr'] = [ordered]@{ version = $HerdrVersion; license = 'Apache-2.0'; source = "https://github.com/herdrdev/herdr/tree/v$HerdrVersion" }
}

# 5. The manifest that makes it a bundle, and the docs and licenses every archive carries.
$manifest = [ordered]@{ bundle = 1; version = $Version; tools = $tools }
Write-Text (Join-Path $Stage 'retake-bundle.json') (($manifest | ConvertTo-Json -Depth 4) + "`n")
foreach ($f in @('README.md', 'CHANGELOG.md', 'LICENSE', 'THIRD_PARTY_NOTICES.md')) {
    $p = Join-Path $Root $f
    if (Test-Path -LiteralPath $p) { Copy-Item -LiteralPath $p -Destination $Stage }
}

# 6. The zip (entries with / between names, at the top, as goreleaser's are) and its checksum line.
$zipName = "${Name}_${Version}_windows_amd64.zip"
$zipPath = Join-Path $OutDir $zipName
if (Test-Path -LiteralPath $zipPath) { Remove-Item -LiteralPath $zipPath -Force }
Add-Type -AssemblyName System.IO.Compression, System.IO.Compression.FileSystem
$stream = [System.IO.File]::Open($zipPath, [System.IO.FileMode]::CreateNew)
$archive = New-Object System.IO.Compression.ZipArchive($stream, [System.IO.Compression.ZipArchiveMode]::Create)
try {
    $prefix = $Stage.TrimEnd('\', '/').Length + 1
    foreach ($f in (Get-ChildItem -LiteralPath $Stage -Recurse -File | Sort-Object FullName)) {
        $entry = $f.FullName.Substring($prefix) -replace '\\', '/'
        [void][System.IO.Compression.ZipFileExtensions]::CreateEntryFromFile($archive, $f.FullName, $entry,
            [System.IO.Compression.CompressionLevel]::Optimal)
    }
} finally {
    $archive.Dispose()
    $stream.Dispose()
}
$line = "$(Get-Sha256 $zipPath)  $zipName"
Write-Text (Join-Path $OutDir 'checksums-windows.txt') "$line`n"
Say "made $zipPath"
Say $line
Get-ChildItem -LiteralPath $Stage -File | ForEach-Object { Say ("  {0,-24} {1,12:N0} bytes" -f $_.Name, $_.Length) }
