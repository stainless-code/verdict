# verdict binary installer (Windows). Downloads one release zip, verifies the
# cosign signature over checksums.txt (when cosign is present) and the archive
# SHA-256, extracts only verdict.exe, and replaces the target atomically.
# No admin/root required, no eval of release metadata.
#
#   irm .../install.ps1 | iex
#   ./install.ps1 -Version 1.2.3 -InstallDir C:\tools\verdict
#   ./install.ps1 -Uninstall [-RemovePath]
#
# Env equivalents: VERDICT_VERSION, VERDICT_INSTALL, VERDICT_DOWNLOAD_URL,
# VERDICT_NO_MODIFY_PATH=1, VERDICT_REQUIRE_SIGNATURE=1, VERDICT_COSIGN.
[CmdletBinding()]
param(
  [string]$Version = $env:VERDICT_VERSION,
  [string]$InstallDir = $env:VERDICT_INSTALL,
  [string]$DownloadUrl = $env:VERDICT_DOWNLOAD_URL,
  [switch]$Uninstall,
  [switch]$RemovePath,
  [switch]$NoModifyPath,
  [switch]$RequireSignature,
  [string]$CosignPath = $env:VERDICT_COSIGN
)
$ErrorActionPreference = 'Stop'
$RepoBase = 'https://github.com/stainless-code/verdict/releases/download'
$LatestUrl = 'https://github.com/stainless-code/verdict/releases/latest'
$ChecksumsName = 'checksums.txt'
$BundleName = 'checksums.txt.sigstore.json'
$IdentityRegexp = '^https://github\.com/stainless-code/verdict/\.github/workflows/release\.yml@refs/tags/v[0-9]+\.[0-9]+\.[0-9]+.*$'
$Issuer = 'https://token.actions.githubusercontent.com'

if ($env:VERDICT_NO_MODIFY_PATH -eq '1') { $NoModifyPath = $true }
if ($env:VERDICT_REQUIRE_SIGNATURE -eq '1') { $RequireSignature = $true }
if (-not $Version) { $Version = 'latest' }
if (-not $DownloadUrl) { $DownloadUrl = $RepoBase }
$DownloadUrl = $DownloadUrl.TrimEnd('/')
if (-not $InstallDir) { $InstallDir = Join-Path $HOME '.verdict\bin' }
if (-not [System.IO.Path]::IsPathRooted($InstallDir)) { throw "VERDICT_INSTALL must be an absolute path (got $InstallDir)" }
if ($InstallDir -match '^[A-Za-z]:\\?$' -or $InstallDir -eq '\') { throw 'refusing to install into a volume root' }
$InstallDir = $InstallDir.TrimEnd('\', '/')
$ReceiptPath = Join-Path $InstallDir '.verdict-installer'
if ($RemovePath -and -not $Uninstall) { throw '-RemovePath requires -Uninstall' }

function Remove-UserPath {
  $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
  $parts = @($userPath -split ';' | Where-Object { $_ -ne '' -and $_ -ne $InstallDir })
  [Environment]::SetEnvironmentVariable('Path', ($parts -join ';'), 'User')
}

function Add-UserPath {
  $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
  $parts = @($userPath -split ';' | Where-Object { $_ -ne '' })
  if ($parts -contains $InstallDir) { return $false }
  [Environment]::SetEnvironmentVariable('Path', (($parts + $InstallDir) -join ';'), 'User')
  if (($env:Path -split ';') -notcontains $InstallDir) { $env:Path = "$InstallDir;$env:Path" }
  return $true
}

function Save-Url {
  param([string]$Url, [string]$OutFile)
  if ($Url -like 'file://*') { Copy-Item -LiteralPath ([uri]$Url).LocalPath -Destination $OutFile -Force; return }
  Invoke-WebRequest -Uri $Url -OutFile $OutFile -UseBasicParsing
}

function Resolve-Latest {
  try { [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12 } catch { }
  $latest = $LatestUrl
  if ($DownloadUrl -ne $RepoBase) { $latest = "$DownloadUrl/latest" }
  $request = [System.Net.HttpWebRequest]::Create($latest)
  $request.AllowAutoRedirect = $false
  $request.Method = 'GET'
  $request.Timeout = 30000
  $request.Proxy = $null
  $response = $null
  try {
    $response = $request.GetResponse()
  } catch [System.Net.WebException] {
    if ($_.Exception.Response) { $response = $_.Exception.Response } else { throw }
  }
  try {
    $location = $response.Headers['Location']
    if (-not $location) { throw "latest did not redirect (GET $latest)" }
    if ($location -match '/v([0-9]+\.[0-9]+\.[0-9]+[^/]*)/?$') { return $Matches[1] }
    throw "could not parse a version from $location"
  } finally {
    if ($response) { $response.Close() }
  }
}

if ($Uninstall) {
  if (-not (Test-Path -LiteralPath $ReceiptPath)) {
    if (Test-Path -LiteralPath (Join-Path $InstallDir 'verdict.exe')) {
      throw "refusing to remove $InstallDir\verdict.exe: no installer receipt (not installed by this script)"
    }
    Write-Host "install: nothing to uninstall in $InstallDir"
  } else {
    Remove-Item -LiteralPath (Join-Path $InstallDir 'verdict.exe') -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $ReceiptPath -Force
    if (-not (Get-ChildItem -LiteralPath $InstallDir -Force -ErrorAction SilentlyContinue)) {
      Remove-Item -LiteralPath $InstallDir -Force -ErrorAction SilentlyContinue
    }
    Write-Host "install: removed $InstallDir\verdict.exe"
  }
  if ($RemovePath) { Remove-UserPath; Write-Host "install: removed $InstallDir from the user PATH" }
  return
}

if ($env:OS -ne 'Windows_NT') {
  throw "install.ps1 supports Windows only; on macOS/Linux use install.sh (supported: windows/amd64, windows/arm64)"
}
$archRaw = [string]$env:PROCESSOR_ARCHITEW6432
if (-not $archRaw) { $archRaw = [string]$env:PROCESSOR_ARCHITECTURE }
switch ($archRaw.ToUpperInvariant()) {
  'AMD64' { $Arch = 'amd64' }
  'ARM64' { $Arch = 'arm64' }
  default {
    throw "unsupported Windows architecture '$archRaw' (supported: windows/amd64, windows/arm64; other OSes use install.sh)"
  }
}

if ($Version -eq 'latest') { $Version = Resolve-Latest } else { $Version = $Version.TrimStart('v') }
if ($Version -notmatch '^[0-9A-Za-z.+-]+$') { throw "bad version: $Version (use X.Y.Z or vX.Y.Z)" }
$ArchiveName = "verdict_${Version}_windows_${Arch}.zip"
$ReleaseUrl = "$DownloadUrl/v$Version"
$ArchivePath = Join-Path ([System.IO.Path]::GetTempPath()) ("verdict-download-" + [guid]::NewGuid().ToString('N') + '.zip')
$Tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("verdict-install-" + [guid]::NewGuid().ToString('N'))
$null = New-Item -ItemType Directory -Path $Tmp -Force
$Stage = $null
try {
  $ChecksumsPath = Join-Path $Tmp $ChecksumsName
  $BundlePath = Join-Path $Tmp $BundleName
  Write-Host "install: downloading verdict $Version (windows/$Arch)"
  try { Save-Url "$ReleaseUrl/$ArchiveName" $ArchivePath } catch {
    throw "download failed: $ReleaseUrl/$ArchiveName (is windows/$Arch published?)"
  }
  try { Save-Url "$ReleaseUrl/$ChecksumsName" $ChecksumsPath } catch { throw "download failed: $ReleaseUrl/$ChecksumsName" }
  $BundleOk = $false
  try { Save-Url "$ReleaseUrl/$BundleName" $BundlePath; $BundleOk = $true } catch { $BundleOk = $false }

  $Cosign = $null
  if ($CosignPath) { if (Test-Path -LiteralPath $CosignPath) { $Cosign = (Resolve-Path -LiteralPath $CosignPath).Path } }
  else { $candidate = Get-Command cosign -ErrorAction SilentlyContinue; if ($candidate) { $Cosign = $candidate.Source } }
  if ($Cosign) {
    if (-not $BundleOk) { throw "cosign is installed but $BundleName is missing; refusing an unverified install" }
    Write-Host "install: verifying the sigstore bundle for $ChecksumsName"
    & $Cosign verify-blob --bundle $BundlePath --certificate-identity-regexp $IdentityRegexp --certificate-oidc-issuer $Issuer $ChecksumsPath
    if ($LASTEXITCODE -ne 0) { throw 'signature verification failed; nothing installed' }
  } elseif ($RequireSignature) {
    throw 'VERDICT_REQUIRE_SIGNATURE=1 but cosign is not installed; nothing installed'
  } else {
    Write-Warning 'cosign not found; verifying SHA-256 only (VERDICT_REQUIRE_SIGNATURE=1 fails closed).'
  }

  $Expected = $null
  foreach ($line in Get-Content -LiteralPath $ChecksumsPath) {
    $parts = $line -split '\s+', 2
    if ($parts.Count -eq 2 -and $parts[1].TrimStart('*') -eq $ArchiveName) { $Expected = $parts[0]; break }
  }
  if (-not $Expected -or $Expected -notmatch '^[0-9a-fA-F]{64}$') {
    throw "$ChecksumsName has no valid entry for $ArchiveName"
  }
  $Actual = (Get-FileHash -LiteralPath $ArchivePath -Algorithm SHA256).Hash.ToLowerInvariant()
  if ($Actual -ne $Expected.ToLowerInvariant()) {
    throw "SHA-256 mismatch for $ArchiveName (expected $Expected, got $Actual); nothing installed"
  }

  $null = New-Item -ItemType Directory -Path $InstallDir -Force
  $Stage = Join-Path $InstallDir ('.verdict-stage-' + [guid]::NewGuid().ToString('N'))
  $null = New-Item -ItemType Directory -Path $Stage
  try { Add-Type -AssemblyName System.IO.Compression.FileSystem -ErrorAction Stop } catch { }
  $zip = $null
  try {
    $zip = [System.IO.Compression.ZipFile]::OpenRead($ArchivePath)
    $allowed = @('verdict.exe', 'LICENSE', 'README.md')
    foreach ($member in $zip.Entries) {
      if ($allowed -notcontains $member.FullName) { throw "archive has an unexpected member: $($member.FullName)" }
    }
    $entry = $null
    foreach ($member in $zip.Entries) {
      if ($member.FullName -eq 'verdict.exe') { $entry = $member; break }
    }
    if (-not $entry) { throw 'archive does not contain verdict.exe' }
    $staged = Join-Path $Stage 'verdict.exe'
    [System.IO.Compression.ZipFileExtensions]::ExtractToFile($entry, $staged, $true)
  } finally {
    if ($zip) { $zip.Dispose() }
  }
  $stream = [System.IO.File]::Open($staged, [System.IO.FileMode]::Open, [System.IO.FileAccess]::ReadWrite)
  try { $stream.Flush($true) } finally { $stream.Dispose() }
  $target = Join-Path $InstallDir 'verdict.exe'
  if (Test-Path -LiteralPath $target) { [System.IO.File]::Replace($staged, $target, $null) }
  else { [System.IO.File]::Move($staged, $target) }
  $receipt = '{"binary":"verdict.exe","version":"' + $Version + '","installed_by":"install.ps1"}'
  [System.IO.File]::WriteAllText($ReceiptPath, $receipt, [System.Text.Encoding]::ASCII)
  Write-Host "install: installed verdict $Version to $target (SHA-256 verified)"

  if ($NoModifyPath) {
    Write-Host "install: user PATH not modified (VERDICT_NO_MODIFY_PATH). Add: $InstallDir"
  } elseif (Add-UserPath) {
    Write-Host "install: added $InstallDir to the user PATH (restart your shell)"
  } else {
    Write-Host "install: $InstallDir is already on the user PATH"
  }
} finally {
  foreach ($item in @($Stage, $Tmp, $ArchivePath)) {
    if ($item -and (Test-Path -LiteralPath $item)) {
      try { Remove-Item -LiteralPath $item -Recurse -Force -ErrorAction Stop } catch { }
    }
  }
}
