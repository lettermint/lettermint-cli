#Requires -Version 7.2
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if ($env:GITHUB_ACTIONS -ne 'true' -or $env:RUNNER_OS -ne 'Windows') {
    throw 'Run this test only on a disposable Windows Actions runner.'
}
$setup = Join-Path $PSScriptRoot 'install-signing-client.ps1'

# Reproduce the failed release with no registered PSGallery, then use the real service.
if (Get-PSRepository -Name PSGallery -ErrorAction SilentlyContinue) {
    Unregister-PSRepository -Name PSGallery -ErrorAction Stop
}
if (Get-PSRepository -Name PSGallery -ErrorAction SilentlyContinue) { throw 'PSGallery was not removed for the test.' }
$global:LASTEXITCODE = 23
& $setup
if ($LASTEXITCODE -ne 0) { throw 'Successful setup kept a stale native process exit code.' }
$module = Get-Module -ListAvailable ArtifactSigning | Where-Object Version -eq ([version]'0.1.8')
if (-not $module) { throw 'The pinned module was not installed.' }
& $setup
Write-Output 'Real missing-repository recovery, module import, and repeated setup passed.'

# Check bounded retries without waiting or making extra network requests.
& {
    $state = @{ Attempts=0; Sleeps=0; Failures=1; Source='https://www.powershellgallery.com/api/v2' }
    function Get-PSRepository {
        [CmdletBinding()] param([string]$Name)
        [pscustomobject]@{ Name='PSGallery'; SourceLocation=$state.Source }
    }
    function Install-Module {
        [CmdletBinding()] param($Name, $RequiredVersion, $Scope, [switch]$Force, $Repository)
        if ($Name -ne 'ArtifactSigning' -or $RequiredVersion -ne '0.1.8' -or $Scope -ne 'CurrentUser' -or $Repository -ne 'PSGallery') {
            throw 'The installer changed its pinned module or repository.'
        }
        $state.Attempts++
        if ($state.Attempts -le $state.Failures) { throw 'Simulated package service failure.' }
    }
    function Start-Sleep { param($Seconds) $state.Sleeps++ }
    & $setup
    if ($state.Attempts -ne 2 -or $state.Sleeps -ne 1) { throw 'Transient failure recovery failed.' }
    $state.Attempts=0; $state.Sleeps=0; $state.Failures=3
    $failed=$false
    try { & $setup } catch { $failed=$true }
    if (-not $failed -or $state.Attempts -ne 3 -or $state.Sleeps -ne 2) { throw 'Persistent failures did not stop after three attempts.' }
    $state.Attempts=0; $state.Sleeps=0; $state.Source='https://packages.example.invalid/api/v2'
    $failed=$false
    try { & $setup } catch [System.Security.SecurityException] { $failed=$true }
    if (-not $failed -or $state.Attempts -ne 0 -or $state.Sleeps -ne 0) { throw 'An unexpected package source was not rejected.' }
}
Write-Output 'Retry limits and package source checks passed.'
