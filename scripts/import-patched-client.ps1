[CmdletBinding()]
param(
	[Parameter(Mandatory = $true)] [string] $Source,
	[switch] $Force
)

# Installs a client copy into client\original as the build baseline. The
# intended input is a previously patched client (an extracted release's
# runtime\client-patched, or a copy that has been played) for people who no
# longer have the pristine original; a pristine client also works. Account
# and machine state accumulated by playing is stripped so the release safety
# invariants in build-release.ps1 still hold.

$ErrorActionPreference = 'Stop'
$workspace = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$destination = [IO.Path]::GetFullPath((Join-Path $workspace 'client\original'))
$sourceRoot = (Resolve-Path -LiteralPath $Source).Path

$destinationPrefix = $destination.TrimEnd([IO.Path]::DirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
$sourcePrefix = $sourceRoot.TrimEnd([IO.Path]::DirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
if ($sourcePrefix -eq $destinationPrefix -or
	$sourcePrefix.StartsWith($destinationPrefix, [StringComparison]::OrdinalIgnoreCase) -or
	$destinationPrefix.StartsWith($sourcePrefix, [StringComparison]::OrdinalIgnoreCase)) {
	throw "Source must be outside client\original: $sourceRoot"
}

# Everything build-release.ps1 reads from the baseline besides the patched
# binaries themselves. Failing here is clearer than failing mid-build.
$requiredRelativePaths = @(
	'Client.exe',
	'Core.dll',
	'QQTDir.dll',
	'QQTModules.dll',
	'QQTSection.dll',
	'config\GameCFG.ini',
	'config\CacheConfig.xml',
	'config\DLScript.xml',
	'DirCfg.ini',
	'p2psvrInfo.ini',
	'caserver.ini',
	'webserver.ini',
	'devConfig.txt',
	'disp_ogl.txt',
	'TenSLX.dat',
	'profile\10000.tpf',
	'QQTLiveUpdateLogFile.log'
)
foreach ($relativePath in $requiredRelativePaths) {
	if (-not (Test-Path -LiteralPath (Join-Path $sourceRoot $relativePath) -PathType Leaf)) {
		throw "Source client is missing required file: $relativePath"
	}
}
if ((Get-Content -LiteralPath (Join-Path $sourceRoot 'QQTLiveUpdateLogFile.log') -Raw) -notmatch 'state\s*=\s*[01]') {
	throw "Source client startup state is invalid: QQTLiveUpdateLogFile.log"
}
$sourceIsPatched = Test-Path -LiteralPath (Join-Path $sourceRoot 'Client.tp-free.json') -PathType Leaf

if ((Test-Path -LiteralPath (Join-Path $destination 'Client.exe') -PathType Leaf) -and -not $Force) {
	throw "client\original already contains a client. Re-run with -Force to replace it."
}

function Test-ZipArchive([string] $Path) {
	try {
		Add-Type -AssemblyName System.IO.Compression.FileSystem
		$archive = [IO.Compression.ZipFile]::OpenRead($Path)
		try {
			$null = $archive.Entries.Count
			return $true
		}
		finally {
			$archive.Dispose()
		}
	}
	catch {
		return $false
	}
}

New-Item -ItemType Directory -Force -Path $destination | Out-Null
Get-ChildItem -LiteralPath $destination -Force |
	Where-Object { $_.Name -ne 'README.md' } |
	Remove-Item -Recurse -Force

Copy-Item -Path (Join-Path $sourceRoot '*') -Destination $destination -Recurse -Force
Get-ChildItem -LiteralPath $destination -Recurse -Force -File | ForEach-Object { $_.IsReadOnly = $false }

# Strip account and machine state a played client accumulates. The lists
# mirror the runtime cleanup and release safety invariants in
# build-release.ps1: anything left here would either leak into the package
# or make the profile/generated-state invariants fail.
$removed = [Collections.Generic.List[string]]::new()
function Remove-ImportedItem([string] $Path) {
	$relativePath = $Path.Substring($destinationPrefix.Length)
	Remove-Item -LiteralPath $Path -Recurse -Force
	[void]$script:removed.Add($relativePath)
}

foreach ($relativePath in @('TenioLog', 'Record', 'qqshow', 'runtime')) {
	$statePath = Join-Path $destination $relativePath
	if (Test-Path -LiteralPath $statePath) { Remove-ImportedItem $statePath }
}
Get-ChildItem -LiteralPath (Join-Path $destination 'profile') -Recurse -Force -File |
	Where-Object { $_.Name -ne '10000.tpf' } |
	ForEach-Object { Remove-ImportedItem $_.FullName }
$dataDirectory = Join-Path $destination 'data'
if (Test-Path -LiteralPath $dataDirectory -PathType Container) {
	Get-ChildItem -LiteralPath $dataDirectory -File -Force |
		Where-Object { $_.Name -match '^\d+-(Friends|kininfo)\.dat$' } |
		ForEach-Object { Remove-ImportedItem $_.FullName }
}
Get-ChildItem -LiteralPath $destination -Recurse -Force -File |
	Where-Object {
		(
			$_.Extension -in @('.log', '.dmp', '.tmp', '.tem') -and
			$_.Name -ne 'QQTLiveUpdateLogFile.log'
		) -or $_.Name -eq 'Thumbs.db'
	} |
	ForEach-Object { Remove-ImportedItem $_.FullName }
$rootLog = Join-Path $destination 'log.txt'
if (Test-Path -LiteralPath $rootLog -PathType Leaf) { Remove-ImportedItem $rootLog }
# Old resource endpoints saved HTML error pages with a .zip suffix. The build
# removes them from the package but restores missing baseline files afterwards,
# so a corrupt archive surviving here would fail the archive invariant.
$downloadCache = Join-Path $destination 'QQTDownloadTemp'
if (Test-Path -LiteralPath $downloadCache -PathType Container) {
	Get-ChildItem -LiteralPath $downloadCache -Recurse -Force -File -Filter '*.zip' |
		Where-Object { -not (Test-ZipArchive $_.FullName) } |
		ForEach-Object { Remove-ImportedItem $_.FullName }
}

Write-Host "Baseline imported: $sourceRoot -> $destination"
if ($sourceIsPatched) {
	Write-Host 'Source is a previously patched client (Client.tp-free.json present). build-release.ps1 detects this: static binary patches verify as already applied and endpoint values already at their target are kept.'
}
else {
	Write-Host 'Source looks like a pristine client (no Client.tp-free.json).'
}
if ($removed.Count -gt 0) {
	Write-Host "Removed $($removed.Count) account/machine state entries:"
	$removed | ForEach-Object { Write-Host "  $_" }
}
else {
	Write-Host 'No account/machine state needed removal.'
}
Write-Host 'devConfig.txt and disp_ogl.txt keep the display settings of the machine the source client ran on; every package build ships the baseline copy of these files.'
Write-Host 'Next: powershell -ExecutionPolicy Bypass -File scripts\build-release.ps1'
