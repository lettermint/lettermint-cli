#Requires -Version 5.1
[CmdletBinding()]
param(
    [ValidatePattern('^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$')][string]$Version = 'REPLACE_WITH_RELEASE_TAG',
    [switch]$AllowDowngrade
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
function Assert-LettermintVersion {
    param([string]$Value)
    $core = '(0|[1-9][0-9]*)'
    if ($Value -cnotmatch ('\Av' + $core + '\.' + $core + '\.' + $core + '(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?\z')) {
        throw 'Use a version such as v1.0.0 or v1.0.0-rc.1.'
    }
    $parts = $Value.Substring(1) -split '-', 2
    if ($parts.Count -eq 2) {
        foreach ($part in ($parts[1] -split '\.')) {
            if ($part -cmatch '\A0[0-9]+\z') { throw 'Numeric pre-release identifiers must not have leading zeroes.' }
        }
    }
}
function Compare-LettermintNumber {
    param([string]$Left, [string]$Right)
    if ($Left.Length -ne $Right.Length) { return [Math]::Sign($Left.Length - $Right.Length) }
    return [string]::CompareOrdinal($Left, $Right)
}
function Compare-LettermintVersion {
    param([string]$Left, [string]$Right)
    Assert-LettermintVersion $Left
    Assert-LettermintVersion $Right
    $leftParts = $Left.Substring(1) -split '-', 2
    $rightParts = $Right.Substring(1) -split '-', 2
    $leftCore = $leftParts[0] -split '\.'
    $rightCore = $rightParts[0] -split '\.'
    for ($i = 0; $i -lt 3; $i++) {
        $order = Compare-LettermintNumber $leftCore[$i] $rightCore[$i]
        if ($order -ne 0) { return $order }
    }
    if ($leftParts.Count -ne $rightParts.Count) { return $rightParts.Count - $leftParts.Count }
    if ($leftParts.Count -eq 1) { return 0 }
    $leftPre = $leftParts[1] -split '\.'
    $rightPre = $rightParts[1] -split '\.'
    for ($i = 0; $i -lt [Math]::Min($leftPre.Count, $rightPre.Count); $i++) {
        $a = $leftPre[$i]; $b = $rightPre[$i]
        if ($a -ceq $b) { continue }
        $aNumber = $a -cmatch '\A[0-9]+\z'
        $bNumber = $b -cmatch '\A[0-9]+\z'
        if ($aNumber -and $bNumber) { return (Compare-LettermintNumber $a $b) }
        if ($aNumber -ne $bNumber) { if ($aNumber) { return -1 }; return 1 }
        return [string]::CompareOrdinal($a, $b)
    }
    return [Math]::Sign($leftPre.Count - $rightPre.Count)
}
function Install-LettermintBinary {
    param([string]$Source, [string]$Target)
    if (Test-Path -LiteralPath $Target) {
        # PowerShell converts $null to an empty string for this .NET parameter.
        [IO.File]::Replace($Source, $Target, [NullString]::Value)
    } else { [IO.File]::Move($Source, $Target) }
}
Assert-LettermintVersion $Version
$signature = Get-AuthenticodeSignature -FilePath $PSCommandPath
if ($signature.Status -ne 'Valid' -or -not $signature.TimeStamperCertificate -or
    -not $signature.SignerCertificate -or
    $signature.SignerCertificate.GetNameInfo([System.Security.Cryptography.X509Certificates.X509NameType]::SimpleName, $false) -cne 'Lettermint B.V.') {
    throw 'Use a signed installer from a Lettermint release.'
}
if ($Version -notmatch '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$') { throw 'Use the installer from a published release or specify -Version.' }
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
    if ($binarySignature.Status -ne 'Valid' -or -not $binarySignature.TimeStamperCertificate -or
        -not $binarySignature.SignerCertificate -or
        $binarySignature.SignerCertificate.Subject -cne $signature.SignerCertificate.Subject) {
        throw 'The binary must have a valid signature from the installer publisher.'
    }
    $reported = & $source version --json | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0 -or $reported.version -ne $number) { throw 'The binary version does not match the requested version.' }
    New-Item -ItemType Directory -Path $bin -Force | Out-Null
    $lock = [IO.File]::Open((Join-Path $root 'install.lock'), 'OpenOrCreate', 'ReadWrite', 'None')
if (Test-Path $marker) {
    $installed = Get-Content -Raw -LiteralPath $marker | ConvertFrom-Json
    if ($installed.manager -ne 'lettermint-powershell') { throw 'Another package manager owns this install.' }
    if (-not $AllowDowngrade -and (Compare-LettermintVersion $Version $installed.version) -lt 0) {
        throw 'Use -AllowDowngrade to install an older version.'
    }
}

    $next = Join-Path $bin 'lettermint.next.exe'
    Copy-Item -LiteralPath $source -Destination $next -Force
    Install-LettermintBinary -Source $next -Target $target
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
