param([Parameter(Mandatory=$true)][string]$Path)
$ErrorActionPreference = 'Stop'
if (-not $env:WINDOWS_SIGN_P12 -or -not $env:WINDOWS_SIGN_PASSWORD) { throw 'Windows signing credentials are required.' }
$bytes = [Convert]::FromBase64String($env:WINDOWS_SIGN_P12)
$certificate = $null
try {
    $certificate = [System.Security.Cryptography.X509Certificates.X509Certificate2]::new(
        $bytes, $env:WINDOWS_SIGN_PASSWORD,
        [System.Security.Cryptography.X509Certificates.X509KeyStorageFlags]::EphemeralKeySet)
    if (-not $certificate.HasPrivateKey) { throw 'The signing certificate has no private key.' }
    $result = Set-AuthenticodeSignature -FilePath $Path -Certificate $certificate -HashAlgorithm SHA256 -TimestampServer 'https://timestamp.digicert.com'
    if ($result.Status -ne 'Valid') { throw "Signature validation failed: $($result.Status)" }
    if (-not $result.TimeStamperCertificate) { throw 'The signature has no trusted timestamp.' }
} finally {
    if ($certificate) { $certificate.Dispose() }
    [Array]::Clear($bytes, 0, $bytes.Length)
}
