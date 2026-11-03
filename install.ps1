# Installs Retake on Windows from a GitHub release, for the current user only (no administrator rights):
#
#   irm https://raw.githubusercontent.com/samahlstrom/retake/main/install.ps1 | iex
#
# or, saved (when security software blocks the line above, or to pick a version):
#
#   curl.exe -fsSLo install.ps1 https://raw.githubusercontent.com/samahlstrom/retake/main/install.ps1
#   powershell -ExecutionPolicy Bypass -File install.ps1 [-Version X.Y.Z] [-Uninstall]
#
# It downloads retake_<version>_windows_amd64.zip and the release's checksums.txt, refuses the zip unless its SHA-256
# matches, and puts its contents (retake.exe, retakew.exe, retake-edit.exe, herdr.exe, ffmpeg.exe, ffprobe.exe, ...)
# in %LOCALAPPDATA%\Programs\retake, replacing an older install there. It adds that folder to your PATH and a Retake
# entry to the Start menu (it runs `retakew.exe start --app`: Retake with no console window, in your browser). A
# running Retake keeps working; the new one is used from its next start. -Uninstall takes all of that away again and
# leaves your settings and videos alone.
#
# Environment (for testing, or a mirror):
#   RETAKE_VERSION    the version to install, as -Version (default: the latest release)
#   INSTALL_DIR       where to install (default: %LOCALAPPDATA%\Programs\retake)
#   INSTALL_REPO      the GitHub owner/repo the releases come from (default: samahlstrom/retake)
#   INSTALL_BASE_URL  the folder holding one release's files instead of GitHub's (https://..., file:///..., or a
#                     folder); {version} in it is replaced by the version
param(
    [string]$Version = $env:RETAKE_VERSION,
    [switch]$Uninstall
)

function Install-Retake {
    param([string]$Version, [switch]$Uninstall)

    # Everything stays inside this function, so `irm | iex` changes nothing in the PowerShell it runs in.
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue'
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

    # The product's name isn't final: these are the places the installer names it.
    $name = 'retake'
    $title = 'Retake'
    $repo = if ($env:INSTALL_REPO) { $env:INSTALL_REPO } else { 'samahlstrom/retake' }
    $releases = "https://github.com/$repo/releases"
    $dir = if ($env:INSTALL_DIR) { $env:INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA "Programs\$name" }
    if (-not [System.IO.Path]::IsPathRooted($dir)) { $dir = Join-Path (Get-Location).Path $dir }
    $dir = [System.IO.Path]::GetFullPath($dir)
    $shortcut = Join-Path ([Environment]::GetFolderPath('Programs')) "$title.lnk"

    function Say([string]$Text) { Write-Host $Text }

    # The user's PATH as stored (unexpanded, so %USERPROFILE%-style entries survive), and writing it back.
    function Get-UserPath {
        $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment')
        if (-not $key) { return '' }
        try { return [string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames) }
        finally { $key.Close() }
    }
    function Set-UserPath([string]$Value) {
        $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment')
        try { $key.SetValue('Path', $Value, [Microsoft.Win32.RegistryValueKind]::ExpandString) }
        finally { $key.Close() }
        # Setting any user variable through .NET tells Explorer and new terminals that the environment changed.
        [Environment]::SetEnvironmentVariable('RETAKE_INSTALL_REFRESH', '1', 'User')
        [Environment]::SetEnvironmentVariable('RETAKE_INSTALL_REFRESH', $null, 'User')
    }
    function Same-Folder([string]$A, [string]$B) {
        $x = [Environment]::ExpandEnvironmentVariables($A).TrimEnd('\')
        $y = [Environment]::ExpandEnvironmentVariables($B).TrimEnd('\')
        return [string]::Equals($x, $y, [StringComparison]::OrdinalIgnoreCase)
    }

    if ($Uninstall) {
        if (Test-Path -LiteralPath $shortcut) { Remove-Item -LiteralPath $shortcut -Force; Say "removed the Start menu entry" }
        $run = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run'
        if ((Get-ItemProperty -Path $run -ErrorAction SilentlyContinue).PSObject.Properties.Name -contains $title) {
            Remove-ItemProperty -Path $run -Name $title
            Say "Retake no longer starts at login"
        }
        $parts = @((Get-UserPath) -split ';' | Where-Object { $_ -and -not (Same-Folder $_ $dir) })
        if ($parts.Count -ne @((Get-UserPath) -split ';' | Where-Object { $_ }).Count) {
            Set-UserPath ($parts -join ';')
            Say "removed $dir from your PATH"
        }
        if (Test-Path -LiteralPath $dir) {
            try {
                Remove-Item -LiteralPath $dir -Recurse -Force
                Say "removed $dir"
            } catch {
                Say "couldn't remove all of $dir (is Retake still running? quit it, then delete the folder): $_"
            }
        }
        Say "Your settings ($env:APPDATA\$name) and Retake's data ($env:LOCALAPPDATA\$name) are still there; delete them if you like."
        return
    }

    $arch = $env:PROCESSOR_ARCHITECTURE
    if ($env:PROCESSOR_ARCHITEW6432) { $arch = $env:PROCESSOR_ARCHITEW6432 }
    if ($arch -ne 'AMD64' -and $arch -ne 'ARM64') { throw "$title runs on 64-bit Windows; this is $arch" }
    if ($arch -eq 'ARM64') { Say "note: this is the x64 build; Windows on ARM runs it emulated" }

    $Version = "$Version".Trim().TrimStart('v')
    if ($Version -and $Version -notmatch '^[0-9]+\.[0-9]+\.[0-9]+[0-9A-Za-z.+-]*$') { throw "-Version wants X.Y.Z, not $Version" }
    if ($env:INSTALL_BASE_URL) {
        if (-not $Version -and $env:INSTALL_BASE_URL.Contains('{version}')) { throw 'INSTALL_BASE_URL has {version}: give -Version too' }
        $base = $env:INSTALL_BASE_URL.Replace('{version}', $Version).TrimEnd('/', '\')
    } elseif ($Version) {
        $base = "$releases/download/v$Version"
    } else {
        $base = "$releases/latest/download"
    }

    $tmp = Join-Path ([System.IO.Path]::GetTempPath()) "$name-install-$([guid]::NewGuid().ToString('N'))"
    New-Item -ItemType Directory -Path $tmp | Out-Null
    try {
        # Fetch puts one of the release's files into $tmp: over https, or copied from a file:/// URL or a folder.
        function Fetch([string]$File) {
            $out = Join-Path $tmp $File
            if ($base -match '^https?://') {
                Invoke-WebRequest -UseBasicParsing -Uri "$base/$File" -OutFile $out
            } else {
                $from = $base
                if ($from -match '^file:') { $from = ([Uri]$from).LocalPath }
                Copy-Item -LiteralPath (Join-Path $from $File) -Destination $out
            }
            return $out
        }

        try { $sums = Get-Content -LiteralPath (Fetch 'checksums.txt') }
        catch { throw "no release at $base (checksums.txt not found): $_" }
        # The zip's line in checksums.txt: "<sha256>  <name>_<version>_windows_amd64.zip". Without -Version, the
        # latest release's checksums.txt says which version it is.
        $want = $null
        $zip = $null
        foreach ($line in $sums) {
            if ($line -notmatch '^([0-9a-fA-F]{64}) [ *]?(\S+)$') { continue }
            $sum = $Matches[1].ToLowerInvariant()
            $file = $Matches[2]
            if ($Version) { $wanted = $file -eq "${name}_${Version}_windows_amd64.zip" }
            else { $wanted = $file -match "^$([regex]::Escape($name))_.+_windows_amd64\.zip$" }
            if ($wanted) {
                $want = $sum
                $zip = $file
                break
            }
        }
        if (-not $zip) { throw "the release at $base has no Windows zip in its checksums.txt" }
        if (-not $Version) { $Version = $zip.Substring($name.Length + 1, $zip.Length - $name.Length - 1 - '_windows_amd64.zip'.Length) }
        Say "$title $Version for Windows"

        $zipPath = Fetch $zip
        $got = (Get-FileHash -Algorithm SHA256 -LiteralPath $zipPath).Hash.ToLowerInvariant()
        if ($got -ne $want) { throw "checksum mismatch for $zip (got $got, want $want): refusing to install it" }
        $new = Join-Path $tmp 'new'
        Expand-Archive -LiteralPath $zipPath -DestinationPath $new
        if (-not (Test-Path -LiteralPath (Join-Path $new "$name.exe"))) { throw "$zip has no $name.exe" }

        # Into place, file by file. A program that is running can't be overwritten on Windows, but it can be renamed:
        # each old file steps aside as <file>.old (removed now, or by the next install once it isn't running).
        New-Item -ItemType Directory -Force -Path $dir | Out-Null
        Get-ChildItem -LiteralPath $dir -Recurse -File -Filter '*.old' | ForEach-Object {
            Remove-Item -LiteralPath $_.FullName -Force -ErrorAction SilentlyContinue
        }
        $prefix = $new.TrimEnd('\').Length + 1
        foreach ($f in (Get-ChildItem -LiteralPath $new -Recurse -File)) {
            $rel = $f.FullName.Substring($prefix)
            $to = Join-Path $dir $rel
            New-Item -ItemType Directory -Force -Path (Split-Path -Parent $to) | Out-Null
            if (Test-Path -LiteralPath $to) {
                $old = "$to.old"
                if (Test-Path -LiteralPath $old) { Remove-Item -LiteralPath $old -Force -ErrorAction SilentlyContinue }
                Move-Item -LiteralPath $to -Destination $old -Force
                Remove-Item -LiteralPath $old -Force -ErrorAction SilentlyContinue
            }
            Move-Item -LiteralPath $f.FullName -Destination $to
        }
        Say "installed $title $Version into $dir (sha256 verified)"

        # PATH (yours, not the machine's), for new terminals and this one.
        $userPath = Get-UserPath
        if (-not (@($userPath -split ';') | Where-Object { $_ -and (Same-Folder $_ $dir) })) {
            Set-UserPath ((@($userPath -split ';' | Where-Object { $_ }) + $dir) -join ';')
            Say "added $dir to your PATH (new terminals have it)"
        }
        if (-not (@($env:Path -split ';') | Where-Object { $_ -and (Same-Folder $_ $dir) })) { $env:Path = "$dir;$env:Path" }

        # The Start menu entry: Retake without a console window.
        $target = Join-Path $dir "${name}w.exe"
        if (-not (Test-Path -LiteralPath $target)) { $target = Join-Path $dir "$name.exe" }
        $shell = New-Object -ComObject WScript.Shell
        $lnk = $shell.CreateShortcut($shortcut)
        $lnk.TargetPath = $target
        $lnk.Arguments = 'start --app'
        $lnk.WorkingDirectory = $env:USERPROFILE
        $lnk.Description = 'Review videos with an AI agent'
        $lnk.IconLocation = "$(Join-Path $dir "$name.exe"),0"
        $lnk.Save()
        Say "added $title to the Start menu"
    } finally {
        Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }

    Say ''
    Say 'Next:'
    Say '  1. An AI agent, if you have none yet (it signs in with its own account):'
    Say '       Claude Code:  irm https://claude.ai/install.ps1 | iex     (it needs Git for Windows: winget install Git.Git)'
    Say '       Codex:        npm install -g @openai/codex'
    Say "  2. Open $title from the Start menu, or run: $name"
}

Install-Retake -Version $Version -Uninstall:$Uninstall
