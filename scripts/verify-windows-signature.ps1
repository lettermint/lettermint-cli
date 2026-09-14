#Requires -Version 5.1
function Assert-LettermintSignature {
    param([Parameter(Mandatory=$true)][string]$Path)
    $signature = Get-AuthenticodeSignature -LiteralPath $Path
    if ($signature.Status -ne 'Valid' -or -not $signature.TimeStamperCertificate -or
        -not $signature.SignerCertificate -or
        $signature.SignerCertificate.GetNameInfo([System.Security.Cryptography.X509Certificates.X509NameType]::SimpleName, $false) -cne 'Lettermint B.V.') {
        throw "Invalid release signature, timestamp, or publisher: $Path"
    }
}
