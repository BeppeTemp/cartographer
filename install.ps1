# Cartographer installer for Windows — install / update / uninstall (D238).
#
# Usage, from PowerShell (Windows PowerShell 5.1 or PowerShell 7):
#   irm https://raw.githubusercontent.com/BeppeTemp/cartographer/main/install.ps1 | iex
#   & ([scriptblock]::Create((irm https://raw.githubusercontent.com/BeppeTemp/cartographer/main/install.ps1))) update
#   & ([scriptblock]::Create((irm https://raw.githubusercontent.com/BeppeTemp/cartographer/main/install.ps1))) uninstall [-BinaryOnly]
#
# The Windows counterpart of install.sh, and deliberately the same contract:
# newest release (pre-releases included), SHA-256 verified against
# sha256sums.txt, per-user destination with no administrator rights, a running
# native service repaired in place through `cartographer upgrade-repair`, and an
# uninstall that refuses while the Scheduled Tasks are still registered.
#
# Environment:
#   CARTOGRAPHER_INSTALL_DIR  target directory (default: %LOCALAPPDATA%\Cartographer\bin)
#   GITHUB_TOKEN              optional token for the release-list call (avoids API rate limits)
#   CARTOGRAPHER_INSTALL_API_URL, CARTOGRAPHER_INSTALL_DOWNLOAD_URL
#                             override the GitHub endpoints; for the test suite
#                             (test/install/windows), not for users
#
# Piped through `iex`, this script runs in the caller's own session. So nothing
# here calls `exit` — that would close the user's window — failures `throw`
# instead, which stops the script and, under `powershell -File`, exits 1; and
# every preference variable is set inside a function, never at script scope,
# so the caller's session is left as it was found.
param(
    [Parameter(Position = 0)]
    [ValidateSet('install', 'update', 'uninstall')]
    [string]$Command = 'install',
    [switch]$BinaryOnly
)

function Get-CartographerInstallDir {
    if ($env:CARTOGRAPHER_INSTALL_DIR) { return $env:CARTOGRAPHER_INSTALL_DIR }
    return (Join-Path $env:LOCALAPPDATA 'Cartographer\bin')
}

function Get-CartographerTarget {
    # The OS architecture, not the process's: an x64 PowerShell on an ARM64
    # machine reports AMD64 in PROCESSOR_ARCHITECTURE and would pick the
    # emulated build. OSArchitecture exists from .NET Framework 4.7.1, which
    # every supported Windows 10/11 ships; the environment is the fallback.
    $arch = $null
    try { $arch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString() } catch { }
    if (-not $arch) {
        $arch = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
    }
    switch -Regex ($arch) {
        '^(Arm64|ARM64)$' { return 'windows-arm64' }
        '^(X64|AMD64)$' { return 'windows-amd64' }
        default { throw "unsupported architecture: $arch" }
    }
}

function Get-CartographerLatestTag([string]$ApiUrl, [string]$TmpDir) {
    # /releases?per_page=1, not /releases/latest: the latter skips
    # pre-releases, and every 0.x release of this project is one (D82, D211).
    $headers = @{ 'User-Agent' = 'cartographer-install.ps1' }
    if ($env:GITHUB_TOKEN) { $headers['Authorization'] = "Bearer $($env:GITHUB_TOKEN)" }
    $out = Join-Path $TmpDir 'releases.json'
    try {
        Invoke-WebRequest -UseBasicParsing -Headers $headers -Uri "$ApiUrl/releases?per_page=1" -OutFile $out
    } catch {
        throw "cannot determine the latest release (rate-limited? set GITHUB_TOKEN): $($_.Exception.Message)"
    }
    # Read from a file rather than .Content: depending on the Content-Type and
    # the PowerShell edition, .Content is a string or a byte array.
    $releases = @(Get-Content -Raw -Path $out | ConvertFrom-Json)
    if ($releases.Count -eq 0 -or -not $releases[0].tag_name) {
        throw 'cannot determine the latest release: the release list is empty'
    }
    return [string]$releases[0].tag_name
}

function Get-CartographerVersion([string]$Exe) {
    if (-not (Test-Path -LiteralPath $Exe)) { return $null }
    # Continue, not the caller's Stop: Windows PowerShell 5.1 turns a native
    # command's redirected stderr into a terminating error under Stop.
    $ErrorActionPreference = 'Continue'
    # Collect the whole output before picking the first line: Select-Object
    # -First in the pipeline stops the native command early, and PowerShell
    # then reports a non-zero $LASTEXITCODE for a run that succeeded.
    $out = @(& $Exe version 2>$null)
    if ($LASTEXITCODE -ne 0 -or $out.Count -eq 0) { return 'unknown' }
    return ([string]$out[0]).Trim()
}

function Test-PathEntry([string]$PathValue, [string]$Dir) {
    $want = $Dir.TrimEnd('\')
    foreach ($entry in ($PathValue -split ';')) {
        if ($entry.TrimEnd('\') -ieq $want) { return $true }
    }
    return $false
}

function Add-CartographerToUserPath([string]$Dir) {
    # The *user* PATH: no administrator rights, nothing outside the profile.
    $user = [Environment]::GetEnvironmentVariable('PATH', 'User')
    if (-not (Test-PathEntry $user $Dir)) {
        $new = if ([string]::IsNullOrEmpty($user)) { $Dir } else { "$($user.TrimEnd(';'));$Dir" }
        [Environment]::SetEnvironmentVariable('PATH', $new, 'User')
        Write-Host "added $Dir to your user PATH"
    }
    # A PATH change reaches new shells only. Under `irm | iex` this *is* the
    # user's shell, so patching the process copy makes `cartographer` work on
    # the very next line instead of looking like a failed install.
    if (-not (Test-PathEntry $env:PATH $Dir)) {
        $env:PATH = "$($env:PATH.TrimEnd(';'));$Dir"
    }
}

function Remove-CartographerFromUserPath([string]$Dir) {
    $user = [Environment]::GetEnvironmentVariable('PATH', 'User')
    if ($user -and (Test-PathEntry $user $Dir)) {
        $want = $Dir.TrimEnd('\')
        $kept = ($user -split ';') | Where-Object { $_ -and ($_.TrimEnd('\') -ine $want) }
        [Environment]::SetEnvironmentVariable('PATH', ($kept -join ';'), 'User')
        Write-Host "removed $Dir from your user PATH"
    }
}

function Remove-CartographerStaleBinaries([string]$Dir) {
    # Best effort: an .old binary is still locked while a server started from
    # it runs, and the next install or uninstall tries again.
    Get-ChildItem -LiteralPath $Dir -Filter 'cartographer.exe.old*' -ErrorAction SilentlyContinue |
        ForEach-Object { Remove-Item -LiteralPath $_.FullName -Force -ErrorAction SilentlyContinue }
}

function Install-Cartographer {
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue'   # Windows PowerShell 5.1 downloads crawl with the progress bar on
    # Windows PowerShell 5.1 still defaults to TLS 1.0 on some hosts, which GitHub refuses.
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

    $apiUrl = if ($env:CARTOGRAPHER_INSTALL_API_URL) { $env:CARTOGRAPHER_INSTALL_API_URL } else { 'https://api.github.com/repos/BeppeTemp/cartographer' }
    $downloadUrl = if ($env:CARTOGRAPHER_INSTALL_DOWNLOAD_URL) { $env:CARTOGRAPHER_INSTALL_DOWNLOAD_URL } else { 'https://github.com/BeppeTemp/cartographer/releases/download' }

    $target = Get-CartographerTarget
    $dir = Get-CartographerInstallDir
    $dest = Join-Path $dir 'cartographer.exe'
    $tmp = Join-Path ([IO.Path]::GetTempPath()) ("cartographer-install-" + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $tmp -Force | Out-Null
    try {
        $tag = Get-CartographerLatestTag $apiUrl $tmp
        $current = Get-CartographerVersion $dest
        if ($current -eq $tag) {
            Write-Host "cartographer $tag already installed at $dest"
            Add-CartographerToUserPath $dir
            return
        }
        if ($current) { Write-Host "updating cartographer $current -> $tag" } else { Write-Host "installing cartographer $tag to $dest" }

        $asset = "cartographer-$target.zip"
        $zip = Join-Path $tmp $asset
        try {
            Invoke-WebRequest -UseBasicParsing -Uri "$downloadUrl/$tag/$asset" -OutFile $zip
        } catch {
            throw "download failed: $downloadUrl/$tag/$asset ($($_.Exception.Message))"
        }

        # Every release that carries Windows zips carries sha256sums.txt, so —
        # stricter than install.sh, which still accepts pre-checksum tags — a
        # missing file is an error too. A file with no line for this asset is
        # the shape a tampered or truncated manifest has.
        $sums = Join-Path $tmp 'sha256sums.txt'
        try {
            Invoke-WebRequest -UseBasicParsing -Uri "$downloadUrl/$tag/sha256sums.txt" -OutFile $sums
        } catch {
            throw "cannot download sha256sums.txt for $tag`: refusing to install unverified"
        }
        $expected = $null
        foreach ($line in (Get-Content -Path $sums)) {
            $fields = $line -split '\s+', 2
            if ($fields.Count -eq 2 -and $fields[1].Trim().TrimStart('*') -eq $asset) { $expected = $fields[0]; break }
        }
        if (-not $expected) { throw "sha256sums.txt has no entry for $asset`: refusing to install unverified" }
        $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $zip).Hash
        if ($actual -ine $expected) { throw "checksum mismatch for $asset" }
        Write-Host 'checksum OK'

        $unpacked = Join-Path $tmp 'unpacked'
        Expand-Archive -LiteralPath $zip -DestinationPath $unpacked -Force
        $newExe = Join-Path $unpacked 'cartographer.exe'
        if (-not (Test-Path -LiteralPath $newExe)) { throw "$asset does not contain cartographer.exe" }

        New-Item -ItemType Directory -Path $dir -Force | Out-Null
        Remove-CartographerStaleBinaries $dir
        if (Test-Path -LiteralPath $dest) {
            # Windows refuses to overwrite or delete a running executable but
            # lets it be renamed. Moving the old one aside is what lets an
            # update land while the native service (a Scheduled Task running
            # this very file) is up, with no stop/start around it;
            # upgrade-repair below then restarts the service on the new file.
            $aside = "$dest.old"
            if (Test-Path -LiteralPath $aside) { $aside = "$dest.old-$([DateTime]::UtcNow.Ticks)" }
            Move-Item -LiteralPath $dest -Destination $aside -Force
        }
        Move-Item -LiteralPath $newExe -Destination $dest -Force
        Write-Host "installed: $(Get-CartographerVersion $dest) -> $dest"
        Add-CartographerToUserPath $dir

        # Repair any native service in place (D121): replaces an already
        # running service, proves the new version is serving, reconciles the
        # configured providers. A no-op when the service is stopped or absent.
        # Continue for the same Windows PowerShell 5.1 stderr reason as in
        # Get-CartographerVersion: the exit code is what decides here.
        $ErrorActionPreference = 'Continue'
        & $dest upgrade-repair
        $rc = $LASTEXITCODE
        switch ($rc) {
            0 { }
            1 { Write-Warning "provider sync is pending — the binary update succeeded; retry with: cartographer sync" }
            2 { throw "new binary installed at $dest but the running native service could not be verified — inspect it (cartographer service status) and retry: cartographer upgrade-repair" }
            default { throw "unexpected exit code $rc from 'cartographer upgrade-repair' — inspect the service and retry" }
        }
        Remove-CartographerStaleBinaries $dir
    } finally {
        Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }
}

function Uninstall-Cartographer([bool]$BinaryOnly) {
    $ErrorActionPreference = 'Stop'
    $dir = Get-CartographerInstallDir
    $dest = Join-Path $dir 'cartographer.exe'

    # The Scheduled Task definitions (D217) are what "installed" means for the
    # native service. Deleting the binary under them leaves tasks pointing at a
    # missing executable, and only Cartographer itself can unregister them.
    $tasks = @('serve.xml', 'sync.xml') |
        ForEach-Object { Join-Path $env:LOCALAPPDATA "cartographer\tasks\$_" } |
        Where-Object { Test-Path -LiteralPath $_ }
    if ($tasks -and -not $BinaryOnly) {
        $lines = @('this machine still has Cartographer Scheduled Tasks registered:')
        $lines += ($tasks | ForEach-Object { "  $_" })
        $lines += ''
        $lines += 'removing only the binary would leave them pointing at a missing executable.'
        $lines += 'Remove them first, with the binary still in place:'
        $lines += '  cartographer service sync-timer uninstall'
        $lines += '  cartographer service uninstall'
        $lines += '  cartographer disconnect            # removes the artifacts materialized into your agents'
        $lines += ''
        $lines += 'then rerun this uninstall; or, to delete the binary anyway and clean up by hand later, add -BinaryOnly'
        throw ($lines -join [Environment]::NewLine)
    }

    if (Test-Path -LiteralPath $dest) {
        try {
            Remove-Item -LiteralPath $dest -Force
        } catch {
            throw "cannot remove $dest — it is in use; stop the server first (cartographer service stop)"
        }
        Write-Host "removed $dest"
    } else {
        Write-Host "cartographer not found in $dir, nothing to do"
    }
    Remove-CartographerStaleBinaries $dir
    Remove-CartographerFromUserPath $dir
    Write-Host 'note: this removes the binary only — materialized agent artifacts and your KB data are untouched.'
}

switch ($Command) {
    'uninstall' { Uninstall-Cartographer $BinaryOnly.IsPresent }
    default { Install-Cartographer }
}
