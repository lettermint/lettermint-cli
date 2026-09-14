#Requires -Version 7.2
param([Parameter(Mandatory=$true)][string]$Path)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
foreach ($name in @('ARTIFACT_SIGNING_ENDPOINT', 'ARTIFACT_SIGNING_ACCOUNT_NAME', 'ARTIFACT_SIGNING_CERTIFICATE_PROFILE_NAME')) {
    if (-not [Environment]::GetEnvironmentVariable($name)) { throw "Missing signing setting: $name" }
}
Import-Module ArtifactSigning -RequiredVersion 0.1.8 -ErrorAction Stop
$signing = @{
    Endpoint = $env:ARTIFACT_SIGNING_ENDPOINT
    CodeSigningAccountName = $env:ARTIFACT_SIGNING_ACCOUNT_NAME
    CertificateProfileName = $env:ARTIFACT_SIGNING_CERTIFICATE_PROFILE_NAME
    Files = (Resolve-Path -LiteralPath $Path).Path
    FileDigest = 'SHA256'
    TimestampRfc3161 = 'http://timestamp.acs.microsoft.com'
    TimestampDigest = 'SHA256'
    ExcludeEnvironmentCredential = $true
    ExcludeWorkloadIdentityCredential = $true
    ExcludeManagedIdentityCredential = $true
    ExcludeSharedTokenCacheCredential = $true
    ExcludeVisualStudioCredential = $true
    ExcludeVisualStudioCodeCredential = $true
    ExcludeAzureCliCredential = $false
    ExcludeAzurePowerShellCredential = $true
    ExcludeAzureDeveloperCliCredential = $true
    ExcludeInteractiveBrowserCredential = $true
}
Invoke-ArtifactSigning @signing
. (Join-Path $PSScriptRoot 'verify-windows-signature.ps1')
Assert-LettermintSignature -Path $Path
