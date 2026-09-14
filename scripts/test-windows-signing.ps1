#Requires -Version 5.1
param([Parameter(Mandatory=$true)][string]$Binary)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
. (Join-Path $PSScriptRoot 'verify-windows-signature.ps1')
$signingTestState = @{ Installer=$null; Binary=$null; Archive=$null; ArchiveName=$null }

function New-TestSignature {
    param([string]$Name = 'Lettermint B.V.', [string]$Thumbprint = 'new-certificate',
          [string]$Status = 'Valid', [bool]$Timestamp = $true,
          [string]$Subject = 'CN=Lettermint B.V., O=Lettermint B.V., C=NL')
    $certificate = [pscustomobject]@{ Name=$Name; Thumbprint=$Thumbprint; Subject=$Subject }
    $certificate | Add-Member -MemberType ScriptMethod -Name GetNameInfo -Value { param($Type, $Issuer) $this.Name }
    [pscustomobject]@{ Status=$Status; SignerCertificate=$certificate; TimeStamperCertificate=$(if ($Timestamp) { 'timestamp' } else { $null }) }
}
function Get-AuthenticodeSignature {
    param([string]$LiteralPath, [string]$FilePath)
    $path = if ($LiteralPath) { $LiteralPath } else { $FilePath }
    if ($path.EndsWith('.exe')) { return $signingTestState.Binary }
    return $signingTestState.Installer
}
$signingTestState.Installer = New-TestSignature -Thumbprint 'installer-certificate'
$signingTestState.Binary = New-TestSignature
Assert-LettermintSignature -Path 'fixture.exe'
foreach ($invalid in @(
    (New-TestSignature -Name 'Another publisher'),
    (New-TestSignature -Name 'Lettermint B.V. Attacker'),
    (New-TestSignature -Status 'HashMismatch'),
    (New-TestSignature -Timestamp $false),
    ([pscustomobject]@{ Status='Valid'; SignerCertificate=$null; TimeStamperCertificate='timestamp' })
)) {
    $signingTestState.Binary = $invalid
    $rejected = $false
    try { Assert-LettermintSignature -Path 'fixture.exe' } catch { $rejected = $true }
    if (-not $rejected) { throw 'An invalid signature passed verification.' }
}

$testRoot = Join-Path ([IO.Path]::GetTempPath()) ('signing caf' + [char]0xE9 + ' ' + [Guid]::NewGuid().ToString('N'))
$previousLocal = $env:LOCALAPPDATA
$env:LOCALAPPDATA = Join-Path $testRoot 'local'
$signingTestState.Archive = Join-Path $testRoot 'fixture.zip'
$binary = (Resolve-Path -LiteralPath $Binary).Path
$reported = & $binary version --json | ConvertFrom-Json
$testVersion = if ($reported.version -eq '0.0.0') { 'v0.0.1' } else { 'v0.0.0' }
# Stop at version validation, after signature verification but before installation.
function Get-Command { param($Name, $CommandType, $ErrorAction) return $null }
function Invoke-WebRequest {
    param([switch]$UseBasicParsing, [string]$Uri, [string]$OutFile)
    if ($Uri.EndsWith('/checksums.txt')) {
        $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $signingTestState.Archive).Hash
        "$hash  $($signingTestState.ArchiveName)" | Set-Content -LiteralPath $OutFile -Encoding ASCII
    } else {
        $signingTestState.ArchiveName = $Uri.Substring($Uri.LastIndexOf('/') + 1)
        Copy-Item -LiteralPath $signingTestState.Archive -Destination $OutFile
    }
}
try {
    New-Item -ItemType Directory -Path (Join-Path $testRoot 'files') -Force | Out-Null
    Copy-Item -LiteralPath $binary -Destination (Join-Path $testRoot 'files/lettermint.exe')
    Compress-Archive -LiteralPath (Join-Path $testRoot 'files/lettermint.exe') -DestinationPath $signingTestState.Archive
    $cases = @(
        @{ Signature=(New-TestSignature); Error='binary version does not match' },
        @{ Signature=(New-TestSignature -Subject 'CN=Another publisher'); Error='installer publisher' },
        @{ Signature=(New-TestSignature -Timestamp $false); Error='installer publisher' },
        @{ Signature=(New-TestSignature -Status 'HashMismatch'); Error='installer publisher' }
    )
    foreach ($case in $cases) {
        $signingTestState.Binary = $case.Signature
        $message = ''
        try { & (Join-Path $PSScriptRoot 'install.ps1') -Version $testVersion } catch { $message = $_.Exception.Message }
        if ($message -notmatch $case.Error) { throw "Unexpected installer result: $message" }
    }
    $signingTestState.Binary = New-TestSignature
    $renderedInstaller = Join-Path $testRoot 'install.ps1'
    $template = [IO.File]::ReadAllText((Join-Path $PSScriptRoot 'install.ps1'))
    [IO.File]::WriteAllText($renderedInstaller, $template.Replace('REPLACE_WITH_RELEASE_TAG', $testVersion))
    $message = ''
    try { & $renderedInstaller } catch { $message = $_.Exception.Message }
    if ($message -notmatch 'binary version does not match') { throw "Embedded installer version failed: $message" }
    $signingTestState.Installer = New-TestSignature -Name 'Another publisher'
    $rejected = $false
    try { & (Join-Path $PSScriptRoot 'install.ps1') -Version $testVersion } catch { $rejected = $_.Exception.Message -match 'signed installer' }
    if (-not $rejected) { throw 'The installer accepted an unrelated publisher.' }
} finally {
    $env:LOCALAPPDATA = $previousLocal
    if (Test-Path $testRoot) { Remove-Item -LiteralPath $testRoot -Recurse -Force }
}
Write-Output 'Windows publisher and certificate rotation checks passed.'
