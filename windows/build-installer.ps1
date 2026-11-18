# Builds Flock-Setup-x64.exe into windows\dist\: the flock CLI, hosts helper and daemon launcher, the tray app
# (via build.ps1, which also runs the Flock.Kit tests) and the Inno Setup installer around them.
#
#   .\windows\build-installer.ps1
#
# Only x64 is built for now; Windows on ARM installs and runs it under emulation.
# Needs Go, the .NET 10 SDK and Inno Setup 6.3 or newer (winget install GoLang.Go Microsoft.DotNet.SDK.10 JRSoftware.InnoSetup).
$ErrorActionPreference = "Stop"

$Arch = "x64"
$repo = Split-Path $PSScriptRoot -Parent
$goArch = "amd64"
# Not VERSION: MSBuild reads environment variables as properties, and a non-semver Version breaks NuGet restore.
$version = $env:FLOCK_VERSION
if (-not $version) { $version = git -C $repo describe --tags --always --dirty 2>$null }
if (-not $version) { $version = "dev" }

# Go and Inno Setup may have been installed after this terminal was opened.
if (-not (Get-Command go -ErrorAction SilentlyContinue)) { $env:Path = "$env:ProgramFiles\Go\bin;$env:Path" }
$iscc = @("$env:LOCALAPPDATA\Programs\Inno Setup 6\ISCC.exe", "${env:ProgramFiles(x86)}\Inno Setup 6\ISCC.exe", "$env:ProgramFiles\Inno Setup 6\ISCC.exe") |
    Where-Object { Test-Path $_ } | Select-Object -First 1
if (-not $iscc) { throw "Inno Setup 6 not found (winget install JRSoftware.InnoSetup)" }

$out = Join-Path $repo "dist\windows-$goArch"
Push-Location $repo
try {
    $env:GOOS = "windows"; $env:GOARCH = $goArch
    go build -ldflags "-s -w -X flock/internal/cli.Version=$version" -o "$out\flock.exe" ./cmd/flock
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    go build -ldflags "-s -w -X main.version=$version" -o "$out\flock-helper.exe" ./cmd/flock-helper
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    go build -ldflags "-s -w -H windowsgui" -o "$out\flockd.exe" ./cmd/flockd
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}
finally {
    Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue
    Pop-Location
}

# Signed when ci-build.yml sets up signing (sign.ps1), like the tray app (build.ps1) and the installer.
$sign = Join-Path $PSScriptRoot "sign.ps1"
& $sign "$out\flock.exe" "$out\flock-helper.exe" "$out\flockd.exe"
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

& (Join-Path $PSScriptRoot "build.ps1")
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

# Inno Setup signs the installer and the uninstaller it contains with the "flock" sign tool ($f: the file).
$signing = @()
if ($env:SIGN_METADATA) { $signing = "/DSign", ('/Sflock=powershell.exe -NoProfile -ExecutionPolicy Bypass -File $q' + $sign + '$q $f') }
& $iscc /Q "/DAppVersion=$version" "/DArch=$Arch" "/DGoArch=$goArch" @signing (Join-Path $PSScriptRoot "installer\Flock.iss")
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "built $(Join-Path $PSScriptRoot "dist\Flock-Setup-$Arch.exe")"
