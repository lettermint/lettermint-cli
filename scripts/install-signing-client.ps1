#Requires -Version 7.2
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

for ($attempt = 1; $attempt -le 3; $attempt++) {
    try {
        $gallery = @(Get-PSRepository -ErrorAction Stop | Where-Object Name -eq 'PSGallery')
        if ($gallery.Count -eq 0) {
            Register-PSRepository -Default -ErrorAction Stop
            $gallery = @(Get-PSRepository -Name PSGallery -ErrorAction Stop)
        }
        if ($gallery.Count -ne 1 -or $gallery[0].SourceLocation.TrimEnd('/') -cne 'https://www.powershellgallery.com/api/v2') {
            throw [System.Security.SecurityException]::new('PSGallery must use the official HTTPS package source.')
        }
        Install-Module -Name ArtifactSigning -RequiredVersion 0.1.8 -Scope CurrentUser -Force -Repository PSGallery -ErrorAction Stop
        $module = Import-Module ArtifactSigning -RequiredVersion 0.1.8 -Force -PassThru -ErrorAction Stop
        if ($module.Version -ne [version]'0.1.8' -or -not $module.ExportedCommands.ContainsKey('Invoke-ArtifactSigning')) {
            throw 'The pinned Artifact Signing client did not load.'
        }
        Write-Output 'ArtifactSigning 0.1.8 is installed and ready.'
        # PackageManagement can leave a native NuGet error code after recovery.
        # Report success only after the exact module and command have loaded.
        exit 0
    } catch [System.Security.SecurityException] {
        throw
    } catch {
        if ($attempt -eq 3) { throw }
        Write-Warning "Artifact Signing setup attempt $attempt failed: $($_.Exception.Message). Retrying."
        Start-Sleep -Seconds (5 * $attempt)
    }
}
