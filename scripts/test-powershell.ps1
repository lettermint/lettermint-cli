param([string]$Binary = (Join-Path (Get-Location) 'lettermint.exe'))
$ErrorActionPreference = 'Stop'
$binary = (Resolve-Path -LiteralPath $Binary).Path
$version = & $binary version --json | ConvertFrom-Json
if ($LASTEXITCODE -ne 0 -or -not $version.version) { throw 'Version output is invalid.' }
$automatic = & $binary version | ConvertFrom-Json
if ($LASTEXITCODE -ne 0 -or -not $automatic.version) { throw 'Piped output did not select JSON.' }
$plain = (& $binary skills list --plain --color always) -join "`n"
if ($LASTEXITCODE -ne 0 -or $plain -notmatch 'lettermint-cli' -or $plain.Contains([string][char]27)) { throw 'Plain output contains invalid terminal formatting.' }
$process = New-Object System.Diagnostics.Process
$process.StartInfo.FileName = $binary
$process.StartInfo.Arguments = 'version --unknown --json'
$process.StartInfo.UseShellExecute = $false
$process.StartInfo.RedirectStandardOutput = $true
$process.StartInfo.RedirectStandardError = $true
[void]$process.Start()
$stdout = $process.StandardOutput.ReadToEnd()
$stderr = $process.StandardError.ReadToEnd()
$process.WaitForExit()
$failure = $stderr | ConvertFrom-Json
if ($process.ExitCode -ne 1 -or $stdout -or $failure.error.code -ne 'command_failed') { throw 'JSON errors are invalid.' }
$process.Dispose()
$path = Join-Path ([IO.Path]::GetTempPath()) ('CLI skills caf' + [char]0xE9 + ' ' + [Guid]::NewGuid().ToString('N'))
try {
    & $binary skills export --output $path --json --no-input | ConvertFrom-Json | Out-Null
    if ($LASTEXITCODE -ne 0 -or -not (Test-Path (Join-Path $path 'lettermint-cli/SKILL.md'))) { throw 'Skill export failed.' }
    $completion = & $binary completion powershell
    if ($LASTEXITCODE -ne 0 -or -not $completion) { throw 'Completion failed.' }
    foreach ($script in @('scripts/install.ps1','scripts/uninstall.ps1','scripts/sign-windows.ps1','scripts/verify-windows-release.ps1','scripts/verify-windows-signature.ps1','scripts/test-windows-signing.ps1')) {
        $tokens = $null; $parseErrors = $null
        [Management.Automation.Language.Parser]::ParseFile((Join-Path (Get-Location) $script), [ref]$tokens, [ref]$parseErrors) | Out-Null
        if ($parseErrors.Count -gt 0) { throw ($parseErrors | Out-String) }
    }
} finally { if (Test-Path $path) { Remove-Item -LiteralPath $path -Recurse -Force } }

& (Join-Path $PSScriptRoot 'test-windows-signing.ps1') -Binary $binary

& (Join-Path $PSScriptRoot 'test-windows-install.ps1')
