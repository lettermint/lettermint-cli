#Requires -Version 5.1
[CmdletBinding(SupportsShouldProcess=$true, ConfirmImpact='High')]
param()
$ErrorActionPreference = 'Stop'
$root = Join-Path $env:LOCALAPPDATA 'Lettermint CLI'
$marker = Join-Path $root 'install.json'
if (-not (Test-Path $marker)) { throw 'No PowerShell-managed install was found.' }
$installed = Get-Content -Raw -LiteralPath $marker | ConvertFrom-Json
if ($installed.manager -ne 'lettermint-powershell') { throw 'Another package manager owns this install.' }
if ($PSCmdlet.ShouldProcess($root, 'Uninstall Lettermint CLI')) {
    $bin = Join-Path $root 'bin'
    $lock = [IO.File]::Open((Join-Path $root 'install.lock'), 'OpenOrCreate', 'ReadWrite', 'None')
    try {
        $path = [Environment]::GetEnvironmentVariable('Path', 'User')
        [Environment]::SetEnvironmentVariable('Path', ((@($path -split ';') | Where-Object { $_ -ne $bin }) -join ';'), 'User')
        foreach ($file in @((Join-Path $bin 'lettermint.exe'), (Join-Path $bin 'lettermint.next.exe'), $marker)) {
            if (Test-Path $file) { Remove-Item -LiteralPath $file -Force }
        }
    } finally { $lock.Dispose() }
    Remove-Item -LiteralPath (Join-Path $root 'install.lock') -Force
    if ((Test-Path $bin) -and @(Get-ChildItem -LiteralPath $bin -Force).Count -eq 0) { Remove-Item -LiteralPath $bin }
    if (@(Get-ChildItem -LiteralPath $root -Force).Count -eq 0) { Remove-Item -LiteralPath $root }
    Write-Output 'The CLI was removed. Saved profiles remain. Use auth logout before uninstall to revoke a grant.'
}
