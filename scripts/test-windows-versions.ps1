#Requires -Version 5.1
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$tokens = $null; $errors = $null
$ast = [Management.Automation.Language.Parser]::ParseFile((Join-Path $PSScriptRoot 'install.ps1'), [ref]$tokens, [ref]$errors)
if ($errors.Count -ne 0) { throw ($errors | Out-String) }
# Load the installer's functions without executing its install or download steps.
$functions = $ast.FindAll({ param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] }, $false)
foreach ($function in $functions) { . ([scriptblock]::Create($function.Extent.Text)) }
$pairs = @(
    @('v1.0.0-alpha', 'v1.0.0-alpha.1'),
    @('v1.0.0-alpha.1', 'v1.0.0-alpha.beta'),
    @('v1.0.0-beta.2', 'v1.0.0-beta.11'),
    @('v1.0.0-rc.1', 'v1.0.0-rc.2'),
    @('v1.0.0-rc.3', 'v1.0.0'),
    @('v1.0.0', 'v1.0.1'),
    @('v1.9.0', 'v1.10.0'),
    @('v2.0.0', 'v10.0.0'),
    @('v1.0.0-Z', 'v1.0.0-a'),
    @('v1.0.0-999999999999999999', 'v1.0.0-1000000000000000000')
)
foreach ($pair in $pairs) {
    if ((Compare-LettermintVersion $pair[0] $pair[1]) -ge 0) { throw "Downgrade order failed: $pair" }
    if ((Compare-LettermintVersion $pair[1] $pair[0]) -le 0) { throw "Upgrade order failed: $pair" }
    if ((Compare-LettermintVersion $pair[0] $pair[0]) -ne 0) { throw "Equal versions failed: $pair" }
}
foreach ($invalid in @('1.0.0', 'v01.0.0', 'v1.0.0-01', 'v1.0.0-a..b', 'v1.0.0+build', "v1.0.0`n")) {
    $rejected = $false
    try { Assert-LettermintVersion $invalid } catch { $rejected = $true }
    if (-not $rejected) { throw "Invalid version accepted: $invalid" }
}
Write-Output 'Windows stable and pre-release version checks passed.'
