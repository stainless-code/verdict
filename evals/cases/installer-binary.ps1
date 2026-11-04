#requires -Version 5.1
# Windows leg of the installer-binary eval: install.ps1 against locally built
# fake release zips served from a loopback HTTP server (no real network).
# Grades: contract archive name, signature argv + fail-closed, checksum
# mismatch, wrong/unknown arch, version pin, latest redirect, PATH opt-outs,
# idempotent re-install, uninstall/foreign refusal, checksum-only fallback.
param(
  [string]$Installer = (Join-Path (Split-Path -Parent (Split-Path -Parent $PSScriptRoot)) 'install.ps1')
)
$ErrorActionPreference = 'Stop'
$RepoRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$script:Fails = 0
function Good { param([string]$Message) Write-Host "  ok: $Message" }
function Bad { param([string]$Message) Write-Host "  FAIL: $Message"; $script:Fails++ }

$Sandbox = Join-Path ([System.IO.Path]::GetTempPath()) ("verdict-ps-eval-" + [guid]::NewGuid().ToString('N'))
$Release = Join-Path $Sandbox 'release'
$FakeBin = Join-Path $Sandbox 'fakebin'
$BinSrc = Join-Path $Sandbox 'fake-verdict.exe'
$RequestLog = Join-Path $Sandbox 'requests.log'
$CosignLog = Join-Path $Sandbox 'cosign.log'
$InstallLog = Join-Path $Sandbox 'install.log'
$script:Installer = $Installer
$script:InstallDir = Join-Path $Sandbox 'install'
$script:Pin = '1.2.3'
$script:Status = 0
$script:BaseUrl = ''
$script:CosignPath = ''
$script:Log = $InstallLog

$originalUserPath = [Environment]::GetEnvironmentVariable('Path', 'User')
$originalProcessPath = $env:Path
$originalArch = $env:PROCESSOR_ARCHITECTURE
$originalArchW6432 = $env:PROCESSOR_ARCHITEW6432
$job = $null
New-Item -ItemType Directory -Path $Release, $FakeBin -Force | Out-Null
# The payload must be a real executable: install.ps1 smokes the installed
# binary's `--version` from its final path, and Windows cannot run a .exe that
# is not a PE. The inbox curl.exe (System32, Windows 10 1803+ and Server 2019+)
# is a self-contained console app that answers `--version` with exit 0 from any
# path. git.exe is not: the Git for Windows launcher resolves its install root
# from its own location and fails with "Top-level not found" once copied.
$payloadExe = Join-Path $env:SystemRoot 'System32\curl.exe'
if (-not (Test-Path -LiteralPath $payloadExe)) { throw "installer-binary needs $payloadExe as the fake payload" }
[System.IO.File]::Copy($payloadExe, $BinSrc, $true)

# Windows PowerShell 5.1 hard-wraps redirected host output at the console
# width, so a long installer line can land split across lines in the log.
# Compare with every whitespace run collapsed to one space on both sides.
function Squash-Space {
  param([string]$Text)
  return [regex]::Replace($Text, '\s+', ' ')
}
function Log-Contains {
  param([string]$Needle)
  if (-not (Test-Path -LiteralPath $script:Log)) { return $false }
  return (Squash-Space ([System.IO.File]::ReadAllText($script:Log))).Contains((Squash-Space $Needle))
}
# Status-Detail: the exit status plus the installer log, so a failed leg says
# why instead of only "status 1".
function Status-Detail {
  $text = ''
  if (Test-Path -LiteralPath $script:Log) { $text = Squash-Space ([System.IO.File]::ReadAllText($script:Log)) }
  return "status $($script:Status): $text"
}
function File-Contains {
  param([string]$Path, [string]$Needle)
  if (-not (Test-Path -LiteralPath $Path)) { return $false }
  return ([System.IO.File]::ReadAllText($Path)).Contains($Needle)
}
function Request-Seen {
  param([string]$Needle)
  if (-not (Test-Path -LiteralPath $RequestLog)) { return $false }
  return ([System.IO.File]::ReadAllText($RequestLog)).Contains($Needle)
}
function Same-Bytes {
  param([string]$Left, [string]$Right)
  if (-not (Test-Path -LiteralPath $Left) -or -not (Test-Path -LiteralPath $Right)) { return $false }
  return ([System.IO.File]::ReadAllText($Left) -eq [System.IO.File]::ReadAllText($Right))
}
function Reset-Install {
  param([string]$Dir = $script:InstallDir)
  if (Test-Path -LiteralPath $Dir) { Remove-Item -LiteralPath $Dir -Recurse -Force }
  Remove-Item -LiteralPath $InstallLog, $CosignLog -Force -ErrorAction SilentlyContinue
  $script:Pin = '1.2.3'
  $script:CosignPath = $fakeCosign
  $env:FAKE_COSIGN_STATUS = '0'
  $env:VERDICT_NO_MODIFY_PATH = '1'
  $env:VERDICT_REQUIRE_SIGNATURE = '0'
}
function Invoke-Installer {
  param(
    [string]$Version = $script:Pin,
    [string]$Dir = $script:InstallDir,
    [switch]$Uninstall,
    [switch]$RemovePath,
    [switch]$NoModifyPath,
    [switch]$RequireSignature,
    [string]$CosignPath = ''
  )
  if (-not $CosignPath) { $CosignPath = $script:CosignPath }
  $params = @{ Version = $Version; InstallDir = $Dir; DownloadUrl = $script:BaseUrl; CosignPath = $CosignPath }
  if ($Uninstall) { $params['Uninstall'] = $true }
  if ($RemovePath) { $params['RemovePath'] = $true }
  if ($NoModifyPath) { $params['NoModifyPath'] = $true }
  if ($RequireSignature) { $params['RequireSignature'] = $true }
  $script:Status = 0
  try {
    & $script:Installer @params *> $script:Log
  } catch {
    $script:Status = 1
    ("installer error: " + ($_ | Out-String)) | Add-Content -LiteralPath $script:Log
  }
}
function New-FakeRelease {
  param([string]$Version, [string]$Goos, [string]$Goarch)
  $dir = Join-Path $Release "v$Version"
  $payload = Join-Path $dir 'payload'
  New-Item -ItemType Directory -Path $payload -Force | Out-Null
  Copy-Item -LiteralPath $BinSrc -Destination (Join-Path $payload 'verdict.exe')
  Set-Content -Path (Join-Path $payload 'LICENSE') -Value 'license' -Encoding ASCII
  Set-Content -Path (Join-Path $payload 'README.md') -Value 'readme' -Encoding ASCII
  Set-Content -Path (Join-Path $payload 'NOTICE.md') -Value 'notice' -Encoding ASCII
  $zip = Join-Path $dir "verdict_${Version}_${Goos}_${Goarch}.zip"
  Remove-Item -LiteralPath $zip -Force -ErrorAction SilentlyContinue
  Push-Location $payload
  try { Compress-Archive -Path 'verdict.exe', 'LICENSE', 'README.md', 'NOTICE.md' -DestinationPath $zip } finally { Pop-Location }
  Remove-Item -LiteralPath $payload -Recurse -Force
}
function Complete-FakeRelease {
  param([string]$Version)
  $dir = Join-Path $Release "v$Version"
  $lines = @(Get-ChildItem -Path (Join-Path $dir 'verdict_*.zip') | ForEach-Object {
    $hash = (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
    "$hash  $($_.Name)"
  })
  Set-Content -Path (Join-Path $dir 'checksums.txt') -Value $lines -Encoding ASCII
  Set-Content -Path (Join-Path $dir 'checksums.txt.sigstore.json') -Value '{}' -Encoding ASCII
}

New-FakeRelease -Version '1.2.3' -Goos windows -Goarch amd64
Complete-FakeRelease '1.2.3'
New-FakeRelease -Version '2.0.0' -Goos windows -Goarch amd64
Complete-FakeRelease '2.0.0'
New-FakeRelease -Version '3.0.0' -Goos windows -Goarch amd64
Complete-FakeRelease '3.0.0'
New-FakeRelease -Version '9.9.9' -Goos windows -Goarch amd64
Complete-FakeRelease '9.9.9'
Set-Content -Path (Join-Path $Release 'v2.0.0/checksums.txt') `
  -Value (('0' * 64) + '  verdict_2.0.0_windows_amd64.zip') -Encoding ASCII

$fakeCosign = Join-Path $FakeBin 'cosign.cmd'
Set-Content -Path $fakeCosign -Encoding ASCII -Value @'
@echo off
echo %*>>"%FAKE_COSIGN_LOG%"
if "%FAKE_COSIGN_STATUS%"=="" exit /b 0
exit /b %FAKE_COSIGN_STATUS%
'@

try {
  $contract = Get-Content -Raw (Join-Path $RepoRoot 'release/contract.json') | ConvertFrom-Json
  $row = @($contract.archives | Where-Object { $_.goos -eq 'windows' -and $_.goarch -eq 'amd64' })[0]
  if (-not $row) { throw 'contract has no windows/amd64 row' }
  $expectedArchive = $row.file.Replace('{version}', '1.2.3')
  $source = [System.IO.File]::ReadAllText($Installer)
  if ($source.Contains($contract.checksums) -and $source.Contains($contract.signatureBundle) `
      -and $source.Contains($contract.cosign.issuer) -and $source.Contains('refs/tags/v') `
      -and $source.Contains('https://github.com/stainless-code/verdict/releases/latest')) {
    Good 'install.ps1 names the contract checksums, bundle, and cosign issuer'
  } else {
    Bad 'install.ps1 drifted from release/contract.json'
  }

  # --- loopback HTTP server over the fake release tree ---
  $probe = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
  $probe.Start()
  $port = $probe.LocalEndpoint.Port
  $probe.Stop()
  $script:BaseUrl = "http://127.0.0.1:$port"
  $serverScript = Join-Path $Sandbox 'server.ps1'
  @'
param([string]$Root, [int]$Port, [string]$Log)
$listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, $Port)
$listener.Start()
try {
  while ($true) {
    $client = $listener.AcceptTcpClient()
    try {
      $stream = $client.GetStream()
      $reader = New-Object System.IO.StreamReader($stream)
      $request = $reader.ReadLine()
      if (-not $request) { continue }
      while ($true) { $header = $reader.ReadLine(); if (-not $header) { break } }
      $target = ($request -split ' ')[1]
      $path = ($target -split '\?')[0]
      Add-Content -LiteralPath $Log -Value $path
      if ($path -eq '/latest') {
        $head = "HTTP/1.1 302 Found`r`nLocation: /v9.9.9`r`nContent-Length: 0`r`nConnection: close`r`n`r`n"
        $body = [byte[]]@()
      } else {
        $file = Join-Path $Root $path.TrimStart('/')
        if (Test-Path -LiteralPath $file -PathType Leaf) {
          $body = [System.IO.File]::ReadAllBytes($file)
          $head = "HTTP/1.1 200 OK`r`nContent-Length: $($body.Length)`r`nConnection: close`r`n`r`n"
        } else {
          $body = [byte[]]@()
          $head = "HTTP/1.1 404 Not Found`r`nContent-Length: 0`r`nConnection: close`r`n`r`n"
        }
      }
      $headBytes = [System.Text.Encoding]::ASCII.GetBytes($head)
      $stream.Write($headBytes, 0, $headBytes.Length)
      if ($body.Length -gt 0) { $stream.Write($body, 0, $body.Length) }
      $stream.Flush()
    } finally {
      $client.Close()
    }
  }
} finally {
  $listener.Stop()
}
'@ | Set-Content -Path $serverScript -Encoding ASCII
  $job = Start-Job -ArgumentList $serverScript, $Release, $port, $RequestLog -ScriptBlock {
    param($ScriptPath, $Root, $Port, $Log)
    & $ScriptPath -Root $Root -Port $Port -Log $Log
  }
  $ready = $false
  for ($i = 0; $i -lt 50; $i++) {
    try {
      $probeClient = [System.Net.Sockets.TcpClient]::new()
      $probeClient.Connect('127.0.0.1', $port)
      $probeClient.Close()
      $ready = $true
      break
    } catch { Start-Sleep -Milliseconds 100 }
  }
  if (-not $ready) { throw "loopback test server did not start: $(Receive-Job $job -Keep 2>&1 | Out-String)" }
  $env:PROCESSOR_ARCHITECTURE = 'AMD64'
  $env:PROCESSOR_ARCHITEW6432 = ''
  $env:FAKE_COSIGN_LOG = $CosignLog

  # --- happy path: contract archive, signature argv, atomic install ---
  Reset-Install
  Invoke-Installer
  $target = Join-Path $script:InstallDir 'verdict.exe'
  if ($script:Status -eq 0 -and (Same-Bytes $BinSrc $target) -and (Request-Seen "/v1.2.3/$expectedArchive")) {
    Good 'windows/amd64 installs the contract archive and expected bytes'
  } else {
    Bad "happy path $(Status-Detail)"
  }
  $receipt = Join-Path $script:InstallDir '.verdict-installer'
  if ((Test-Path -LiteralPath $receipt) -and ((Get-Content -Raw $receipt | ConvertFrom-Json).version -eq '1.2.3')) {
    Good 'installer receipt records the pinned version'
  } else {
    Bad 'installer receipt missing or wrong version'
  }
  if (Log-Contains '(SHA-256 verified; reports ') {
    Good 'installer smokes the installed binary and reports its --version'
  } else {
    Bad "no post-verify --version smoke: $(Get-Content -Raw $InstallLog -ErrorAction SilentlyContinue)"
  }
  if (Log-Contains 'Next: verdict skill install --detect') {
    Good 'installer points at the skill next step'
  } else {
    Bad "no skill next step: $(Get-Content -Raw $InstallLog -ErrorAction SilentlyContinue)"
  }
  if ((File-Contains $CosignLog '--certificate-identity-regexp') -and (File-Contains $CosignLog 'refs/tags/v') `
      -and (File-Contains $CosignLog '--certificate-oidc-issuer') -and (File-Contains $CosignLog $contract.cosign.issuer)) {
    Good 'cosign is invoked with the contract identity and issuer'
  } else {
    Bad "cosign argv missing identity/issuer: $(Get-Content -Raw $CosignLog -ErrorAction SilentlyContinue)"
  }

  # --- checksum and signature failures leave the old binary in place ---
  Copy-Item -LiteralPath $target -Destination (Join-Path $Sandbox 'old.exe')
  $script:Pin = '2.0.0'
  Invoke-Installer
  if ($script:Status -ne 0 -and (Log-Contains 'SHA-256 mismatch') -and (Same-Bytes (Join-Path $Sandbox 'old.exe') $target)) {
    Good 'checksum mismatch refuses and leaves the old binary'
  } else {
    Bad "checksum mismatch $(Status-Detail)"
  }
  if (Log-Contains 'Next:') {
    Bad 'a refused install printed a next step'
  } else {
    Good 'a refused install prints no next step'
  }
  $script:Pin = '1.2.3'
  Invoke-Installer
  Copy-Item -LiteralPath $target -Destination (Join-Path $Sandbox 'old2.exe')
  $script:Pin = '2.0.0'
  $env:FAKE_COSIGN_STATUS = '1'
  Invoke-Installer
  if ($script:Status -ne 0 -and (Log-Contains 'signature verification failed') -and (Same-Bytes (Join-Path $Sandbox 'old2.exe') $target)) {
    Good 'bad signature refuses before replacing the binary'
  } else {
    Bad "bad signature $(Status-Detail)"
  }
  $env:FAKE_COSIGN_STATUS = '0'
  if (Get-ChildItem -LiteralPath $script:InstallDir -Filter '.verdict-stage-*' -Force -ErrorAction SilentlyContinue) {
    Bad 'refused runs left a staging dir'
  } else {
    Good 'refused runs clean staging behind'
  }

  # --- wrong and unknown architectures fail with the supported list ---
  $env:PROCESSOR_ARCHITECTURE = 'ARM64'
  Invoke-Installer
  if ($script:Status -ne 0 -and (Log-Contains 'download failed')) {
    Good 'an unpublished windows/arm64 archive refuses the download'
  } else {
    Bad "wrong-arch $(Status-Detail)"
  }
  $env:PROCESSOR_ARCHITECTURE = 'IA64'
  Invoke-Installer
  if ($script:Status -ne 0 -and (Log-Contains 'supported: windows/amd64, windows/arm64')) {
    Good 'unknown Windows architecture refuses with the supported list'
  } else {
    Bad "unknown-arch $(Status-Detail)"
  }
  $env:PROCESSOR_ARCHITECTURE = 'AMD64'

  # --- version pin and latest redirect ---
  Reset-Install
  Invoke-Installer -Version 'v3.0.0'
  $receipt = Join-Path $script:InstallDir '.verdict-installer'
  if ($script:Status -eq 0 -and ((Get-Content -Raw $receipt | ConvertFrom-Json).version -eq '3.0.0') `
      -and (Request-Seen '/v3.0.0/')) {
    Good 'Version v3.0.0 pins the release'
  } else {
    Bad "version pin $(Status-Detail)"
  }
  Reset-Install
  Invoke-Installer -Version 'latest'
  $receipt = Join-Path $script:InstallDir '.verdict-installer'
  if ($script:Status -eq 0 -and ((Get-Content -Raw $receipt | ConvertFrom-Json).version -eq '9.9.9') `
      -and (Request-Seen '/latest') -and (Request-Seen '/v9.9.9/')) {
    Good 'latest resolves through the releases redirect'
  } else {
    Bad "latest $(Status-Detail)"
  }

  # --- PATH: one append, switch/env opt-outs, removal ---
  Reset-Install
  $env:VERDICT_NO_MODIFY_PATH = '0'
  Invoke-Installer
  $parts = @(([Environment]::GetEnvironmentVariable('Path', 'User')) -split ';' | Where-Object { $_ -ne '' })
  $count = @($parts | Where-Object { $_ -eq $script:InstallDir }).Count
  Invoke-Installer
  $partsAgain = @(([Environment]::GetEnvironmentVariable('Path', 'User')) -split ';' | Where-Object { $_ -ne '' })
  $countAgain = @($partsAgain | Where-Object { $_ -eq $script:InstallDir }).Count
  if ($count -eq 1 -and $countAgain -eq 1) {
    Good 'user PATH gains one entry and re-install does not duplicate it'
  } else {
    Bad "user PATH append count $count -> $countAgain"
  }
  $dir2 = Join-Path $Sandbox 'install-no-path'
  Invoke-Installer -Dir $dir2 -NoModifyPath
  if (-not (@(([Environment]::GetEnvironmentVariable('Path', 'User')) -split ';') -contains $dir2)) {
    Good '-NoModifyPath leaves the user PATH alone'
  } else {
    Bad '-NoModifyPath still edited the user PATH'
  }
  $dir3 = Join-Path $Sandbox 'install-env-no-path'
  $env:VERDICT_NO_MODIFY_PATH = '1'
  Invoke-Installer -Dir $dir3
  if (-not (@(([Environment]::GetEnvironmentVariable('Path', 'User')) -split ';') -contains $dir3)) {
    Good 'VERDICT_NO_MODIFY_PATH=1 leaves the user PATH alone'
  } else {
    Bad 'env PATH opt-out still edited the user PATH'
  }
  $env:VERDICT_NO_MODIFY_PATH = '1'
  Invoke-Installer -Dir $script:InstallDir -Uninstall -RemovePath
  $restored = @(([Environment]::GetEnvironmentVariable('Path', 'User')) -split ';' | Where-Object { $_ -ne '' }) -join ';'
  $original = @($originalUserPath -split ';' | Where-Object { $_ -ne '' }) -join ';'
  if ($script:Status -eq 0 -and $restored -eq $original) {
    Good 'uninstall -RemovePath restores the original user PATH'
  } else {
    Bad 'uninstall -RemovePath did not restore the user PATH'
  }

  # --- idempotent re-install and uninstall/foreign refusal ---
  Reset-Install
  Invoke-Installer
  Copy-Item -LiteralPath $target -Destination (Join-Path $Sandbox 'first.exe')
  Invoke-Installer
  if ($script:Status -eq 0 -and (Same-Bytes (Join-Path $Sandbox 'first.exe') $target) `
      -and -not (Get-ChildItem -LiteralPath $script:InstallDir -Filter '.verdict-stage-*' -Force -ErrorAction SilentlyContinue)) {
    Good 're-install is idempotent and leaves no staging dirs'
  } else {
    Bad "re-install $(Status-Detail)"
  }
  Set-Content -Path (Join-Path $script:InstallDir 'notes.txt') -Value 'foreign' -Encoding ASCII
  Invoke-Installer -Uninstall
  if ($script:Status -eq 0 -and -not (Test-Path -LiteralPath $target) `
      -and -not (Test-Path -LiteralPath (Join-Path $script:InstallDir '.verdict-installer')) `
      -and (Test-Path -LiteralPath (Join-Path $script:InstallDir 'notes.txt'))) {
    Good 'uninstall removes managed files and leaves foreign files'
  } else {
    Bad "uninstall $(Status-Detail)"
  }
  $foreignDir = Join-Path $Sandbox 'foreign'
  New-Item -ItemType Directory -Path $foreignDir -Force | Out-Null
  Set-Content -Path (Join-Path $foreignDir 'verdict.exe') -Value 'foreign' -Encoding ASCII
  Invoke-Installer -Dir $foreignDir -Uninstall
  if ($script:Status -ne 0 -and (Log-Contains 'refusing to remove') -and (Test-Path -LiteralPath (Join-Path $foreignDir 'verdict.exe'))) {
    Good 'uninstall refuses a foreign verdict.exe without a receipt'
  } else {
    Bad "foreign uninstall $(Status-Detail)"
  }

  # --- checksum-only fallback and require-signature fail-closed ---
  Reset-Install
  Invoke-Installer -CosignPath (Join-Path $Sandbox 'no-cosign.exe')
  if ($script:Status -eq 0 -and (Log-Contains 'SHA-256 only')) {
    Good 'missing cosign falls back to checksum-only with a loud warning'
  } else {
    Bad "checksum-only fallback $(Status-Detail)"
  }
  Reset-Install
  Invoke-Installer -CosignPath (Join-Path $Sandbox 'no-cosign.exe') -RequireSignature
  if ($script:Status -ne 0 -and (Log-Contains 'VERDICT_REQUIRE_SIGNATURE=1')) {
    Good 'RequireSignature fails closed without cosign'
  } else {
    Bad "require-signature $(Status-Detail)"
  }
} finally {
  if ($job) { Stop-Job $job -ErrorAction SilentlyContinue; Remove-Job $job -Force -ErrorAction SilentlyContinue }
  [Environment]::SetEnvironmentVariable('Path', $originalUserPath, 'User')
  $env:Path = $originalProcessPath
  $env:PROCESSOR_ARCHITECTURE = $originalArch
  $env:PROCESSOR_ARCHITEW6432 = $originalArchW6432
  Remove-Item -LiteralPath $Sandbox -Recurse -Force -ErrorAction SilentlyContinue
}

if ($script:Fails -ne 0) { Write-Host "case installer-binary (windows): FAIL ($script:Fails)"; exit 1 }
Write-Host 'case installer-binary (windows): PASS'
