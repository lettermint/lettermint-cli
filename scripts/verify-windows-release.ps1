#Requires -Version 5.1
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$assets = (Resolve-Path 'release-bundle/assets').Path
$binary = (Resolve-Path 'smoke/lettermint.exe').Path
$tag = (Get-Content -Raw $env:GITHUB_EVENT_PATH | ConvertFrom-Json).release.tag_name
. (Join-Path $PSScriptRoot 'verify-windows-signature.ps1')
foreach ($path in @($binary, (Join-Path $assets 'install.ps1'), (Join-Path $assets 'uninstall.ps1'))) {
    Assert-LettermintSignature -Path $path
}
$reported = & $binary version --json | ConvertFrom-Json
if ($LASTEXITCODE -ne 0 -or $reported.version -ne $tag.Substring(1)) { throw 'Incorrect release version.' }
& ./scripts/test-powershell.ps1 -Binary $binary

# Use the unmodified signed scripts with the local, verified release files.
# Only the download command is replaced. Signature and checksum checks still run.
function Invoke-WebRequest {
    param([switch]$UseBasicParsing, [string]$Uri, [string]$OutFile)
    $prefix = "https://github.com/lettermint/lettermint-cli/releases/download/$tag/"
    if (-not $Uri.StartsWith($prefix)) { throw 'Unexpected installer download URL.' }
    $name = $Uri.Substring($prefix.Length)
    if ($name.Contains('/') -or $name.Contains('\')) { throw 'Invalid installer file name.' }
    Copy-Item -LiteralPath (Join-Path $assets $name) -Destination $OutFile
}
$previousLocal = $env:LOCALAPPDATA
$previousPath = [Environment]::GetEnvironmentVariable('Path', 'User')
$testRoot = Join-Path ([IO.Path]::GetTempPath()) ('Lettermint caf' + [char]0xE9 + ' ' + [Guid]::NewGuid().ToString('N'))
$env:LOCALAPPDATA = $testRoot
$install = Join-Path $assets 'install.ps1'
$uninstall = Join-Path $assets 'uninstall.ps1'
$root = Join-Path $testRoot 'Lettermint CLI'
$target = Join-Path $root 'bin/lettermint.exe'
$marker = Join-Path $root 'install.json'
try {
    & $install -Version $tag
    if (-not (Test-Path $target)) { throw 'Install failed.' }
    @{ manager='lettermint-powershell'; version='v0.0.0' } | ConvertTo-Json | Set-Content $marker -Encoding UTF8
    & $install -Version $tag
    if ((Get-Content -Raw $marker | ConvertFrom-Json).version -ne $tag) { throw 'Upgrade failed.' }
    & $install -Version $tag
    @{ manager='lettermint-powershell'; version='v99999.0.0' } | ConvertTo-Json | Set-Content $marker -Encoding UTF8
    $blocked = $false
    try { & $install -Version $tag } catch { $blocked = $_.Exception.Message -match 'AllowDowngrade' }
    if (-not $blocked) { throw 'An implicit downgrade was not blocked.' }
    & $install -Version $tag -AllowDowngrade
    @{ manager='other'; version=$tag } | ConvertTo-Json | Set-Content $marker -Encoding UTF8
    $blocked = $false
    try { & $install -Version $tag } catch { $blocked = $_.Exception.Message -match 'package manager' }
    if (-not $blocked) { throw 'The installer changed another package manager installation.' }
    @{ manager='lettermint-powershell'; version=$tag } | ConvertTo-Json | Set-Content $marker -Encoding UTF8
    & $uninstall -Confirm:$false
    if ((Test-Path $target) -or (Test-Path $marker)) { throw 'Uninstall failed.' }
} finally {
    [Environment]::SetEnvironmentVariable('Path', $previousPath, 'User')
    $env:LOCALAPPDATA = $previousLocal
    Remove-Item Function:Invoke-WebRequest
    if (Test-Path $testRoot) { Remove-Item -LiteralPath $testRoot -Recurse -Force }
}
