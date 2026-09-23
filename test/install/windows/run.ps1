# test/install/windows/run.ps1 - install.ps1 end to end on a real Windows host (D238).
#
# Usage (CI's test-windows job; needs python on PATH for the fixture server):
#   pwsh -File test/install/windows/run.ps1 -Shell pwsh -NewBin <v9.9.9 exe> -OldBin <v9.9.8 exe>
#   pwsh -File test/install/windows/run.ps1 -Shell powershell ...   # Windows PowerShell 5.1
#
# The counterpart of test/install/run.sh, with the difference that matters on
# Windows: nothing here is faked except the release server. The binaries are
# real builds, the zip is a real zip, and the script runs under the shell a user
# would run it in - so what a POSIX host cannot see (a running executable being
# locked, the per-user PATH in the registry, Windows PowerShell 5.1's own
# quirks) is exercised for real.
param(
    [Parameter(Mandatory)] [ValidateSet('pwsh', 'powershell')] [string]$Shell,
    [Parameter(Mandatory)] [string]$NewBin,
    [Parameter(Mandatory)] [string]$OldBin
)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$repo = (Resolve-Path (Join-Path $PSScriptRoot '..\..\..')).Path
$installer = Join-Path $repo 'install.ps1'
$tag = 'v9.9.9'
$asset = if ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture -eq 'Arm64') { 'cartographer-windows-arm64.zip' } else { 'cartographer-windows-amd64.zip' }

$script:failed = 0
function Pass([string]$m) { Write-Host "  PASS  $m" }
function Fail([string]$m) { Write-Host "  FAIL  $m"; $script:failed++ }
function Check([bool]$ok, [string]$m) { if ($ok) { Pass $m } else { Fail $m } }

# --- fixture release server --------------------------------------------------
$root = Join-Path ([IO.Path]::GetTempPath()) ("cg-install-test-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path (Join-Path $root 'srv\api') -Force | Out-Null
"[{`"tag_name`": `"$tag`", `"prerelease`": true}]" | Set-Content -Path (Join-Path $root 'srv\api\releases') -NoNewline

$stage = Join-Path $root 'stage'
New-Item -ItemType Directory -Path $stage -Force | Out-Null
Copy-Item $NewBin (Join-Path $stage 'cartographer.exe')
$zip = Join-Path $root $asset
Compress-Archive -Path (Join-Path $stage '*') -DestinationPath $zip
$hash = (Get-FileHash -Algorithm SHA256 $zip).Hash.ToLower()

# One download base per checksum case; the release list is shared.
$cases = @{
    'valid'    = "$hash  $asset"
    'mismatch' = "$('0' * 64)  $asset"
    'noentry'  = "$('1' * 64)  some-other-asset.zip"
}
foreach ($case in $cases.Keys) {
    $d = Join-Path $root "srv\$case\$tag"
    New-Item -ItemType Directory -Path $d -Force | Out-Null
    Copy-Item $zip (Join-Path $d $asset)
    $cases[$case] | Set-Content -Path (Join-Path $d 'sha256sums.txt')
}

$port = Get-Random -Minimum 20000 -Maximum 40000
$server = Start-Process -PassThru -WindowStyle Hidden -FilePath python -ArgumentList @('-m', 'http.server', "$port", '--bind', '127.0.0.1', '--directory', (Join-Path $root 'srv'))
for ($i = 0; $i -lt 50; $i++) {
    try { Invoke-WebRequest -UseBasicParsing "http://127.0.0.1:$port/api/releases" | Out-Null; break } catch { Start-Sleep -Milliseconds 200 }
}

# --- isolation ----------------------------------------------------------------
# A private LOCALAPPDATA and profile, so the default install dir, the task
# files the uninstall looks for and anything the real binary reads are all
# inside the fixture. The user PATH is the registry's and is restored at the end.
$savedUserPath = [Environment]::GetEnvironmentVariable('PATH', 'User')
$env:LOCALAPPDATA = Join-Path $root 'LocalAppData'
$env:USERPROFILE = Join-Path $root 'profile'
New-Item -ItemType Directory -Path $env:LOCALAPPDATA, $env:USERPROFILE -Force | Out-Null
Remove-Item Env:\CARTOGRAPHER_INSTALL_DIR -ErrorAction SilentlyContinue
$env:CARTOGRAPHER_INSTALL_API_URL = "http://127.0.0.1:$port/api"
$installDir = Join-Path $env:LOCALAPPDATA 'Cartographer\bin'
$dest = Join-Path $installDir 'cartographer.exe'

function Invoke-Installer([string]$case, [string[]]$arguments) {
    $env:CARTOGRAPHER_INSTALL_DOWNLOAD_URL = "http://127.0.0.1:$port/$case"
    # The installer's stderr is captured with 2>&1; under Stop that would turn
    # its first error line into a terminating error in *this* script.
    $ErrorActionPreference = 'Continue'
    $out = & $Shell -NoProfile -ExecutionPolicy Bypass -File $installer @arguments 2>&1 | Out-String
    return @{ rc = $LASTEXITCODE; out = $out }
}
function Installed-Version { if (Test-Path $dest) { (& $dest version | Select-Object -First 1).Trim() } }
function UserPath-Has([string]$dir) {
    ([Environment]::GetEnvironmentVariable('PATH', 'User') -split ';') -contains $dir
}

try {
    Write-Host "=== install.ps1 under $Shell ==="

    Write-Host '--- fresh install'
    $r = Invoke-Installer 'valid' @()
    Check ($r.rc -eq 0) "exits 0 (rc=$($r.rc))"
    Check ((Installed-Version) -eq $tag) "installs $tag into the default per-user dir"
    Check ($r.out -match 'checksum OK') 'verifies the checksum'
    Check (UserPath-Has $installDir) 'adds the install dir to the user PATH'
    if ($r.rc -ne 0) { Write-Host $r.out }

    Write-Host '--- already current'
    $r = Invoke-Installer 'valid' @('update')
    Check ($r.rc -eq 0 -and $r.out -match 'already installed') 'a second run is a no-op'

    Write-Host '--- update over an older binary'
    Copy-Item $OldBin $dest -Force
    $r = Invoke-Installer 'valid' @('update')
    Check ($r.rc -eq 0 -and $r.out -match 'updating cartographer v9.9.8 -> v9.9.9') 'reports the version change'
    Check ((Installed-Version) -eq $tag) "replaces it with $tag"
    Check (-not (Test-Path "$dest.old")) 'leaves no .old binary when nothing held the old one'

    Write-Host '--- update while the old binary is running'
    # A running executable cannot be overwritten on Windows: this is the
    # native-service case, where the Scheduled Task runs this very file.
    Copy-Item $OldBin $dest -Force
    $kb = Join-Path $root 'kb'
    $running = Start-Process -PassThru -WindowStyle Hidden -FilePath $dest -ArgumentList @('serve', '--kb', $kb, '--init', '--http', "127.0.0.1:$($port + 1)")
    Start-Sleep -Seconds 2
    Check (-not $running.HasExited) 'the old binary is running and holds its file'
    $r = Invoke-Installer 'valid' @('update')
    Check ($r.rc -eq 0) "exits 0 (rc=$($r.rc))"
    Check ((Installed-Version) -eq $tag) "installs $tag over a running executable"
    Stop-Process -Id $running.Id -Force -ErrorAction SilentlyContinue
    $running.WaitForExit(10000) | Out-Null
    if ($r.rc -ne 0) { Write-Host $r.out }

    Write-Host '--- checksum failures install nothing'
    Copy-Item $OldBin $dest -Force
    $r = Invoke-Installer 'mismatch' @('update')
    Check ($r.rc -ne 0 -and $r.out -match 'checksum mismatch') 'a wrong digest is a stop'
    Check ((Installed-Version) -eq 'v9.9.8') 'the previous binary is untouched'
    $r = Invoke-Installer 'noentry' @('update')
    Check ($r.rc -ne 0 -and $r.out -match 'no entry for') 'a manifest without this asset is a stop'
    Check ((Installed-Version) -eq 'v9.9.8') 'the previous binary is still untouched'

    Write-Host '--- uninstall'
    $tasks = Join-Path $env:LOCALAPPDATA 'cartographer\tasks'
    New-Item -ItemType Directory -Path $tasks -Force | Out-Null
    Set-Content -Path (Join-Path $tasks 'serve.xml') -Value '<Task/>'
    $r = Invoke-Installer 'valid' @('uninstall')
    Check ($r.rc -ne 0 -and $r.out -match 'cartographer service uninstall') 'refuses while a Scheduled Task is registered, naming the fix'
    Check (Test-Path $dest) 'and keeps the binary'
    $r = Invoke-Installer 'valid' @('uninstall', '-BinaryOnly')
    Check ($r.rc -eq 0 -and -not (Test-Path $dest)) '-BinaryOnly removes it anyway'
    Remove-Item -Recurse -Force $tasks
    Copy-Item $NewBin $dest -Force
    $r = Invoke-Installer 'valid' @('uninstall')
    Check ($r.rc -eq 0 -and -not (Test-Path $dest)) 'a clean uninstall removes the binary'
    Check (-not (UserPath-Has $installDir)) 'and the user PATH entry'
} finally {
    [Environment]::SetEnvironmentVariable('PATH', $savedUserPath, 'User')
    Stop-Process -Id $server.Id -Force -ErrorAction SilentlyContinue
    Remove-Item -Recurse -Force $root -ErrorAction SilentlyContinue
}

if ($script:failed -gt 0) {
    Write-Host "[install.ps1/$Shell] FAIL - $($script:failed) assertion(s) failed"
    exit 1
}
Write-Host "[install.ps1/$Shell] PASS"
