#Requires -Version 5.1
[CmdletBinding()]
param(
    [Parameter(Mandatory=$true)][ValidatePattern('^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$')][string]$Version,
    [switch]$AllowDowngrade
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
$signature = Get-AuthenticodeSignature -FilePath $PSCommandPath
if ($signature.Status -ne 'Valid') { throw 'Use a signed installer from a Lettermint release.' }
$root = Join-Path $env:LOCALAPPDATA 'Lettermint CLI'
$bin = Join-Path $root 'bin'
$marker = Join-Path $root 'install.json'
$target = Join-Path $bin 'lettermint.exe'
$existing = Get-Command lettermint -CommandType Application -ErrorAction SilentlyContinue
if ($existing -and $existing.Source -ne $target) { throw 'Lettermint is managed elsewhere. Use that package manager to update it.' }
if ((Test-Path $root) -and -not (Test-Path $marker)) { throw 'The install directory has no ownership record.' }
$architecture = $env:PROCESSOR_ARCHITECTURE
if ($env:PROCESSOR_ARCHITEW6432) { $architecture = $env:PROCESSOR_ARCHITEW6432 }
$arch = switch ($architecture) { 'ARM64' { 'arm64' }; 'AMD64' { 'amd64' }; default { throw 'This architecture is not supported.' } }
$number = $Version.Substring(1)
$archive = "lettermint_${number}_windows_${arch}.zip"
$url = "https://github.com/lettermint/lettermint-cli/releases/download/$Version"
$temp = Join-Path ([IO.Path]::GetTempPath()) ('lettermint-' + [Guid]::NewGuid().ToString('N'))
$lock = $null
try {
    New-Item -ItemType Directory -Path $temp | Out-Null
    Invoke-WebRequest -UseBasicParsing -Uri "$url/$archive" -OutFile (Join-Path $temp $archive)
    Invoke-WebRequest -UseBasicParsing -Uri "$url/checksums.txt" -OutFile (Join-Path $temp 'checksums.txt')
    $matches = @(Get-Content (Join-Path $temp 'checksums.txt') | Where-Object { $_ -match ('^[a-fA-F0-9]{64}\s+\*?' + [regex]::Escape($archive) + '$') })
    if ($matches.Count -ne 1) { throw 'The archive checksum is missing or ambiguous.' }
    $expected = ($matches[0] -split '\s+')[0]
    if ((Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $temp $archive)).Hash -ne $expected) { throw 'The archive checksum does not match.' }
    Expand-Archive -LiteralPath (Join-Path $temp $archive) -DestinationPath (Join-Path $temp 'files')
    $source = Join-Path $temp 'files/lettermint.exe'
    $binarySignature = Get-AuthenticodeSignature -FilePath $source
    if ($binarySignature.Status -ne 'Valid' -or $binarySignature.SignerCertificate.Thumbprint -ne $signature.SignerCertificate.Thumbprint) {
        throw 'The binary must have a valid signature from the installer publisher.'
    }
    $reported = & $source version --json | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0 -or $reported.version -ne $number) { throw 'The binary version does not match the requested version.' }
    New-Item -ItemType Directory -Path $bin -Force | Out-Null
    $lock = [IO.File]::Open((Join-Path $root 'install.lock'), 'OpenOrCreate', 'ReadWrite', 'None')
if (Test-Path $marker) {
    $installed = Get-Content -Raw -LiteralPath $marker | ConvertFrom-Json
    if ($installed.manager -ne 'lettermint-powershell') { throw 'Another package manager owns this install.' }
    if (-not $AllowDowngrade -and [version](($Version -replace '^v','') -split '-')[0] -lt [version](($installed.version -replace '^v','') -split '-')[0]) {
        throw 'Use -AllowDowngrade to install an older version.'
    }
}

    $next = Join-Path $bin 'lettermint.next.exe'
    Copy-Item -LiteralPath $source -Destination $next -Force
    if (Test-Path $target) {
        [IO.File]::Replace($next, $target, $null)
    } else { [IO.File]::Move($next, $target) }
    @{ manager='lettermint-powershell'; version=$Version } | ConvertTo-Json | Set-Content -LiteralPath $marker -Encoding UTF8
    $path = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (@($path -split ';') -notcontains $bin) {
        [Environment]::SetEnvironmentVariable('Path', ((([string]$path).TrimEnd(';') + ';' + $bin).TrimStart(';')), 'User')
    }
    Write-Output "Installed Lettermint $Version. Open a new terminal."
} finally {
    if ($lock) { $lock.Dispose() }
    if (Test-Path $temp) { Remove-Item -LiteralPath $temp -Recurse -Force }
}
