[CmdletBinding()]
param(
    [string] $Name = 'QQTang-Local',
    # Build only the Windows artifacts: skip the Linux ONNX Runtime downloads,
    # the Linux amd64/arm64 server cross-builds (which need Linux cross GCC
    # toolchains), and the Linux runtime/config/start-script payload.
    [switch] $WindowsOnly
)

$ErrorActionPreference = 'Stop'
$packageName = $Name
$workspace = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$releaseRoot = [IO.Path]::GetFullPath((Join-Path $workspace 'release'))
$target = [IO.Path]::GetFullPath((Join-Path $releaseRoot $packageName))
$baselineClient = [IO.Path]::GetFullPath((Join-Path $workspace 'client\original'))
if (-not (Test-Path -LiteralPath (Join-Path $baselineClient 'Client.exe') -PathType Leaf)) {
	throw "Original client is missing. Read client\original\README.md and extract the supported client into $baselineClient"
}
# A previously patched client (imported with scripts\import-patched-client.ps1)
# is a supported baseline: the static binary patchers detect already-applied
# bytes, and Set-ReleaseIniExactValue keeps values already at their target.
$baselineIsPatched = Test-Path -LiteralPath (Join-Path $baselineClient 'Client.tp-free.json') -PathType Leaf
if ($baselineIsPatched) {
	Write-Host 'Baseline client\original is a previously patched client; patches already in place are verified instead of re-applied.'
}
$releasePrefix = $releaseRoot.TrimEnd([IO.Path]::DirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
if (-not $target.StartsWith($releasePrefix, [StringComparison]::OrdinalIgnoreCase)) {
    throw "Release target escapes the workspace release directory: $target"
}

function Assert-LinuxServerBinary([string] $Path, [int] $ExpectedMachine, [string] $Architecture) {
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) { throw "Linux $Architecture server is missing: $Path" }
    $bytes = [IO.File]::ReadAllBytes($Path)
    if ($bytes.Length -lt 20 -or $bytes[0] -ne 0x7F -or $bytes[1] -ne 0x45 -or $bytes[2] -ne 0x4C -or $bytes[3] -ne 0x46) {
        throw "Linux $Architecture server is not an ELF executable: $Path"
    }
    if ($bytes[4] -ne 2 -or $bytes[5] -ne 1) {
        throw "Linux $Architecture server is not a 64-bit little-endian ELF executable: $Path"
    }
    $machine = [int]$bytes[18] -bor ([int]$bytes[19] -shl 8)
    if ($machine -ne $ExpectedMachine) {
        throw "Linux server architecture mismatch for $Path`: ELF machine $machine, expected $ExpectedMachine ($Architecture)"
    }
}

function Test-ZipArchive([string] $Path) {
    try {
        Add-Type -AssemblyName System.IO.Compression.FileSystem
        $archive = [IO.Compression.ZipFile]::OpenRead($Path)
        try {
            # Opening the central directory is enough to reject the HTML error
            # pages that an old resource endpoint saved with a .zip suffix.
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

function Set-ReleaseIniValues([string] $Path, [hashtable] $Values) {
	$encoding = [Text.Encoding]::GetEncoding(936)
	$text = [IO.File]::ReadAllText($Path, $encoding)
	foreach ($name in $Values.Keys) {
		$pattern = '(?m)^(' + [Regex]::Escape($name) + '=).*$'
		if (-not [Regex]::IsMatch($text, $pattern)) { throw "Expected key $name was not found in $Path" }
		$text = [Regex]::Replace($text, $pattern, ('${1}' + $Values[$name]))
	}
	[IO.File]::WriteAllText($Path, $text, $encoding)
}

function Set-ReleaseIniExactValue([string] $Path, [string] $Name, [string] $OldValue, [string] $NewValue) {
	$encoding = [Text.Encoding]::GetEncoding(936)
	$text = [IO.File]::ReadAllText($Path, $encoding)
	$pattern = '(?m)^(' + [Regex]::Escape($Name) + '=)' + [Regex]::Escape($OldValue) + '\r?$'
	if (-not [Regex]::IsMatch($text, $pattern)) {
		# A baseline imported from a previously patched client already carries
		# the target value; only that exact value is accepted as satisfied.
		$patchedPattern = '(?m)^' + [Regex]::Escape($Name) + '=' + [Regex]::Escape($NewValue) + '\r?$'
		if ([Regex]::IsMatch($text, $patchedPattern)) { return }
		# Otherwise the baseline is neither the pristine supported client nor a
		# patched one; report what the file actually contains so a wrong
		# extract is obvious.
		$keyPattern = '(?mi)^[ \t]*' + [Regex]::Escape($Name) + '[ \t]*=[^\r\n]*'
		$actualLines = [Regex]::Matches($text, $keyPattern) | ForEach-Object { $_.Value.Trim() }
		$detail = if ($actualLines) { 'actual: ' + ($actualLines -join '; ') } else { "no $Name line exists" }
		throw "Expected value $Name=$OldValue was not found in $Path ($detail)"
	}
	$text = [Regex]::Replace($text, $pattern, ('${1}' + $NewValue))
	[IO.File]::WriteAllText($Path, $text, $encoding)
}

function Set-ReleaseClientFrameRate([string] $Path, [int] $FrameRate) {
	$encoding = [Text.Encoding]::GetEncoding(936)
	$text = [IO.File]::ReadAllText($Path, $encoding)
	$settingPattern = '(?mi)^([ \t]*limitfps[ \t]*=[ \t]*)[^\r\n]*'
	$matches = [Regex]::Matches($text, $settingPattern)
	if ($matches.Count -gt 1) {
		throw "Expected at most one limitfps setting in $Path, found $($matches.Count)"
	}
	if ($matches.Count -eq 1) {
		$text = [Regex]::Replace($text, $settingPattern, ('${1}' + $FrameRate))
	}
	else {
		$newline = if ($text.Contains("`r`n")) { "`r`n" } else { "`n" }
		$optionsPattern = '(?m)^\[options\][ \t]*\r?$'
		if (-not [Regex]::IsMatch($text, $optionsPattern)) {
			throw "Expected [options] section was not found in $Path"
		}
		$text = [Regex]::Replace($text, $optionsPattern, ('$0' + $newline + 'limitfps=' + $FrameRate), 1)
	}
	[IO.File]::WriteAllText($Path, $text, $encoding)
}

if (-not $WindowsOnly) {
	$ensureONNXRuntimeLinux = Join-Path $workspace 'scripts\ensure-onnxruntime-linux.ps1'
	if (-not (Test-Path -LiteralPath $ensureONNXRuntimeLinux -PathType Leaf)) {
		throw "Linux ONNX Runtime dependency installer is missing: $ensureONNXRuntimeLinux"
	}
	& $ensureONNXRuntimeLinux
}

$required = @(
	(Join-Path $workspace 'build-assets\client-no-tp\Client.exe'),
	(Join-Path $workspace 'build-assets\client-no-tp\Client.tp-free.json'),
	(Join-Path $workspace 'build-assets\resource-cache\item-zips'),
	(Join-Path $workspace 'build-assets\resource-cache\pet-model-zips'),
    (Join-Path $baselineClient 'Client.exe'),
    (Join-Path $workspace 'configs\server-directory-local-ui.json'),
    (Join-Path $workspace 'configs\adventure-rules.json'),
    (Join-Path $workspace 'configs\role-rules.json'),
    (Join-Path $workspace 'configs\starter-loadouts.json'),
    (Join-Path $workspace 'configs\break-egg-rewards.json'),
	(Join-Path $workspace 'configs\item-image-aliases.json'),
	(Join-Path $workspace 'configs\item-resource-manifest-overrides.json'),
	(Join-Path $workspace 'configs\item-resource-variant-preferences.json'),
    (Join-Path $workspace 'configs\network.json'),
	(Join-Path $workspace 'configs\models\qqtang-rule1.onnx'),
	(Join-Path $workspace 'configs\models\qqtang-rule1.onnx.json'),
	(Join-Path $workspace 'build-assets\onnxruntime\windows-amd64\onnxruntime.dll'),
	(Join-Path $workspace 'build-assets\onnxruntime\windows-amd64\onnxruntime_providers_shared.dll'),
	(Join-Path $workspace 'data\qqt_combine_recipes.json'),
	(Join-Path $workspace 'data\qqt_forge_rules.json'),
    (Join-Path $workspace 'configs\sso-message-trace-stable.json'),
    (Join-Path $workspace 'deploy\windows\README.txt'),
    (Join-Path $workspace 'deploy\windows\RELEASE.md')
	(Join-Path $workspace 'deploy\windows\start-server-windows.cmd')
	(Join-Path $workspace 'scripts\ensure-client-resource-aliases.ps1')
	(Join-Path $workspace 'cmd\qqt-tp-compat-dll\main.go')
	(Join-Path $workspace 'cmd\qqt-static-shop-server\main.go')
	(Join-Path $workspace 'cmd\qqt-static-multi-client\main.go')
	(Join-Path $workspace 'cmd\qqt-static-solo-boss-card\main.go')
	(Join-Path $workspace 'cmd\qqt-static-room-properties\main.go')
	(Join-Path $workspace 'cmd\qqt-static-start-rejection\main.go')
	(Join-Path $workspace 'cmd\qqt-static-profile-persistence\main.go')
	(Join-Path $workspace 'scripts\patch-python23-item-registry.py')
	(Join-Path $workspace 'cmd\qqt-static-avatar-forge\main.go')
	(Join-Path $workspace 'cmd\qqt-static-platform-effects\main.go')
	(Join-Path $workspace 'cmd\qqt-static-frame-rate\main.go')
	(Join-Path $workspace 'cmd\qqt-item-resources\main.go')
	(Join-Path $workspace 'cmd\qqt-pet-resources\main.go')
	(Join-Path $workspace 'cmd\qqt-launcher-winforms\Program.cs')
	(Join-Path $workspace 'cmd\qqt-launcher-winforms\QQTang.ico')
	(Join-Path $workspace 'cmd\qqt-launcher-winforms\QQTang-Launcher.exe.manifest')
)
if (-not $WindowsOnly) {
	$required += @(
		(Join-Path $workspace 'build-assets\onnxruntime\linux-amd64\libonnxruntime.so.1.29.0'),
		(Join-Path $workspace 'build-assets\onnxruntime\linux-amd64\libonnxruntime_providers_shared.so'),
		(Join-Path $workspace 'build-assets\onnxruntime\linux-arm64\libonnxruntime.so.1.29.0'),
		(Join-Path $workspace 'build-assets\onnxruntime\linux-arm64\libonnxruntime_providers_shared.so'),
		(Join-Path $workspace 'deploy\linux\start-server-linux-amd64.sh'),
		(Join-Path $workspace 'deploy\linux\start-server-linux-arm64.sh')
	)
}
foreach ($path in $required) {
    if (-not (Test-Path -LiteralPath $path)) { throw "Release dependency is missing: $path" }
}

if (Test-Path -LiteralPath $target) {
    # $target was resolved and verified above to be a child of workspace\release.
    Remove-Item -LiteralPath $target -Recurse -Force
}
$directories = @(
    $target,
    (Join-Path $target 'configs'),
	(Join-Path $target 'data'),
    (Join-Path $target 'scripts'),
    (Join-Path $target 'runtime\bin'),
    (Join-Path $target 'runtime\client-patched'),
    (Join-Path $target 'runtime\data'),
    (Join-Path $target 'runtime\logs'),
    (Join-Path $target 'runtime\patches')
)
New-Item -ItemType Directory -Force -Path $directories | Out-Null

# Build only from immutable/reproducible inputs. The live development client is
# never read: original files come from the verified baseline, expanded assets
# come from the independent resource cache, and every patch is applied below.
$targetClient = Join-Path $target 'runtime\client-patched'
Copy-Item -Path (Join-Path $baselineClient '*') -Destination $targetClient -Recurse -Force
Get-ChildItem -LiteralPath $targetClient -Recurse -Force -File | ForEach-Object { $_.IsReadOnly = $false }
$sourcePlaceholder = Join-Path $targetClient 'README.md'
if (Test-Path -LiteralPath $sourcePlaceholder -PathType Leaf) {
	Remove-Item -LiteralPath $sourcePlaceholder -Force
}
Copy-Item -LiteralPath (Join-Path $workspace 'build-assets\client-no-tp\Client.exe') -Destination (Join-Path $targetClient 'Client.exe') -Force
Copy-Item -LiteralPath (Join-Path $workspace 'build-assets\client-no-tp\Client.tp-free.json') -Destination (Join-Path $targetClient 'Client.tp-free.json') -Force
Set-ReleaseClientFrameRate (Join-Path $targetClient 'config\GameCFG.ini') 144

$networkSettings = Get-Content -Raw -LiteralPath (Join-Path $workspace 'configs\network.json') | ConvertFrom-Json
$clientAddress = [string]$networkSettings.client_server_ip
$parsedClientAddress = $null
if (-not [Net.IPAddress]::TryParse($clientAddress, [ref]$parsedClientAddress)) {
	$parsedClientAddress = [Net.Dns]::GetHostAddresses($clientAddress) |
		Where-Object { $_.AddressFamily -eq [Net.Sockets.AddressFamily]::InterNetwork } |
		Select-Object -First 1
	if (-not $parsedClientAddress) { throw "Client endpoint $clientAddress has no IPv4 address" }
}
$clientIPv4 = $parsedClientAddress.ToString()
$dirConfig = Join-Path $targetClient 'DirCfg.ini'
$p2pConfig = Join-Path $targetClient 'p2psvrInfo.ini'
$caConfig = Join-Path $targetClient 'caserver.ini'
$webConfig = Join-Path $targetClient 'webserver.ini'
Set-ReleaseIniValues $dirConfig @{ DirIP = $clientIPv4 }
Set-ReleaseIniExactValue $dirConfig 'DirPort' '80' '18080'
Set-ReleaseIniExactValue $dirConfig 'DirPort' '8000' '18000'
Set-ReleaseIniExactValue $dirConfig 'DirPort' '443' '18443'
Set-ReleaseIniValues $p2pConfig @{ tcpip = $clientIPv4; udpip = $clientIPv4; stunip = $clientIPv4 }
Set-ReleaseIniExactValue $p2pConfig 'tcpport' '443' '18443'
Set-ReleaseIniExactValue $p2pConfig 'udpport' '8000' '18000'
Set-ReleaseIniExactValue $p2pConfig 'stunport' '8000' '18000'
Set-ReleaseIniValues $caConfig @{ dn = 'localhost'; ip = $clientIPv4; ip2 = $clientIPv4 }
Set-ReleaseIniExactValue $caConfig 'port' '80' '18080'
Set-ReleaseIniExactValue $caConfig 'port2' '7000' '17000'
Set-ReleaseIniValues $webConfig @{ dn = 'localhost'; ip = $clientIPv4; ip2 = $clientIPv4 }
Set-ReleaseIniExactValue $webConfig 'port' '80' '18080'
Set-ReleaseIniExactValue $webConfig 'port2' '8080' '18080'

# The reconstructed Client.exe has a normal import table and never enters the
# legacy TP bootstrap. Do not distribute the dormant protection executable,
# user-mode modules, helper, or kernel driver.
foreach ($name in @('ClientBase.dll', 'TerSafe.dll', 'TenSLX.dll', 'TP3Helper.exe', 'TesSafe.sys')) {
    Remove-Item -LiteralPath (Join-Path $targetClient $name) -Force -ErrorAction SilentlyContinue
}
Get-ChildItem -LiteralPath $targetClient -File -Filter 'Client.no-tp.experimental*.exe' -ErrorAction SilentlyContinue |
    Remove-Item -Force
$preferredGoPath = if ($env:QQTANG_GO) {
    $env:QQTANG_GO
}
else {
    # The bundled toolchain convention places go1.26.7 next to the workspace's
    # grandparent. A workspace checked out at a drive root has no grandparent;
    # fall back to the go command on PATH instead of joining an empty path.
    $workspaceParent = Split-Path $workspace
    $workspaceGrandparent = if ($workspaceParent) { Split-Path $workspaceParent } else { '' }
    if ($workspaceGrandparent) { Join-Path $workspaceGrandparent 'go1.26.7\bin\go.exe' } else { '' }
}
$go = if ($preferredGoPath -and (Test-Path -LiteralPath $preferredGoPath -PathType Leaf)) {
    Get-Command $preferredGoPath -ErrorAction Stop
}
else {
    Get-Command go -ErrorAction Stop
}
$gcc = Get-Command gcc -ErrorAction SilentlyContinue
if (-not $gcc) {
    $wingetRoot = Join-Path $env:LOCALAPPDATA 'Microsoft\WinGet\Packages'
    $gccPath = Get-ChildItem -Path (Join-Path $wingetRoot 'BrechtSanders.WinLibs.POSIX.UCRT_*\mingw64\bin\gcc.exe') -File -ErrorAction SilentlyContinue |
        Select-Object -First 1 -ExpandProperty FullName
    if ($gccPath) { $gcc = Get-Command $gccPath -ErrorAction Stop }
}
if (-not $gcc) { throw 'ONNX Runtime release build requires a Windows amd64 GCC toolchain for CGO.' }
$pythonExe = $null
$pythonArgs = @()
$pythonCandidates = @()
$pyLauncher = Get-Command py -ErrorAction SilentlyContinue
if ($pyLauncher) { $pythonCandidates += @{ Exe = $pyLauncher.Source; Args = @('-3') } }
foreach ($pythonName in @('python3', 'python')) {
    $pythonCommand = Get-Command $pythonName -ErrorAction SilentlyContinue
    if ($pythonCommand -and $pythonCommand.Source -notlike '*\Microsoft\WindowsApps\*') {
        $pythonCandidates += @{ Exe = $pythonCommand.Source; Args = @() }
    }
}
foreach ($pythonCandidate in $pythonCandidates) {
    & $pythonCandidate.Exe $pythonCandidate.Args -c 'import sys; sys.exit(0 if sys.version_info >= (3, 8) else 1)' 2>$null
    if ($LASTEXITCODE -eq 0) {
        $pythonExe = $pythonCandidate.Exe
        $pythonArgs = $pythonCandidate.Args
        break
    }
}
if (-not $pythonExe) {
    throw 'The Python 2.3 item-registry patch needs a working Python 3.8+ interpreter; none was found. The Microsoft Store python.exe alias stub does not count. Install Python 3 (winget install Python.Python.3.12 or https://www.python.org/downloads/windows/), then reopen the shell and rerun the build.'
}
$goCache = Join-Path $workspace '.cache\go-build'
New-Item -ItemType Directory -Force -Path $goCache | Out-Null
$env:GOCACHE = $goCache
Push-Location $workspace
try {
	$itemResourceIndex = Join-Path $target 'runtime\patches\item-resource-index.json'
	$itemResourceReport = Join-Path $target 'runtime\patches\item-resource-restoration.md'
	$itemResourceDownloadLog = Join-Path $target 'runtime\patches\item-resource-download-results.json'
	& $go.Source run ./cmd/qqt-item-resources `
		-client-root $targetClient `
		-cache-root (Join-Path $workspace 'build-assets\resource-cache\item-zips') `
		-json $itemResourceIndex `
		-report $itemResourceReport `
		-download-log $itemResourceDownloadLog `
		-manifest-overrides (Join-Path $workspace 'configs\item-resource-manifest-overrides.json') `
		-variant-preferences (Join-Path $workspace 'configs\item-resource-variant-preferences.json') `
		-install
	if ($LASTEXITCODE -ne 0) { throw "Official item resource restoration exited with $LASTEXITCODE" }
	$itemResourceEvidence = Get-Content -Raw -Encoding UTF8 -LiteralPath $itemResourceIndex | ConvertFrom-Json
	foreach ($incompleteState in @('missing', 'partial', 'unmapped')) {
		$count = [int]($itemResourceEvidence.summary.resource_status_counts.$incompleteState)
		if ($count -ne 0) { throw "Official item resource restoration left $count resources in state $incompleteState" }
	}

	$petResourceReport = Join-Path $target 'runtime\patches\pet-resource-restoration.json'
	& $go.Source run ./cmd/qqt-pet-resources `
		-client-root $targetClient `
		-cache-root (Join-Path $workspace 'build-assets\resource-cache\pet-model-zips') `
		-json $petResourceReport `
		-install
	if ($LASTEXITCODE -ne 0) { throw "Official pet resource restoration exited with $LASTEXITCODE" }
	$petResourceEvidence = Get-Content -Raw -Encoding UTF8 -LiteralPath $petResourceReport | ConvertFrom-Json
	$failedPetResources = @($petResourceEvidence.results | Where-Object {
		$_.error -or $_.download_state -eq 'missing' -or $_.install_state -notin @('installed', 'existing')
	})
	if ($failedPetResources.Count -ne 0) {
		throw "Official pet resource restoration left $($failedPetResources.Count) incomplete models"
	}

	& (Join-Path $workspace 'scripts\ensure-client-resource-aliases.ps1') -ClientRoot $targetClient

	$soloBossPatchReport = Join-Path $target 'runtime\patches\qqtsection-solo-boss-card.json'
	& $go.Source run ./cmd/qqt-static-solo-boss-card -client-root $targetClient -out $soloBossPatchReport
	if ($LASTEXITCODE -ne 0) { throw "QQTSection static single-player Boss-card patch exited with $LASTEXITCODE" }
	& $go.Source run ./cmd/qqt-static-solo-boss-card -check -client-root $targetClient
	if ($LASTEXITCODE -ne 0) { throw "QQTSection static single-player Boss-card verification exited with $LASTEXITCODE" }
	& $pythonExe $pythonArgs (Join-Path $workspace 'scripts\patch-python23-item-registry.py') --client-root $targetClient --xdis-root (Join-Path $workspace 'build-assets\pytools')
	if ($LASTEXITCODE -ne 0) { throw "Python 2.3 item-registry patch exited with $LASTEXITCODE" }
	& $pythonExe $pythonArgs (Join-Path $workspace 'scripts\patch-python23-item-registry.py') --check --client-root $targetClient --xdis-root (Join-Path $workspace 'build-assets\pytools')
	if ($LASTEXITCODE -ne 0) { throw "Python 2.3 item-registry verification exited with $LASTEXITCODE" }
	$soloBossPatchEvidence = Get-Content -Raw -LiteralPath $soloBossPatchReport | ConvertFrom-Json
	$soloBossPatchEvidence.client_root = 'runtime/client-patched'
	$soloBossPatchEvidence.dll_path = 'runtime/client-patched/QQTSection.dll'
	$soloBossPatchEvidence | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $soloBossPatchReport -Encoding utf8
	$roomPropertiesPatchReport = Join-Path $target 'runtime\patches\qqtsection-room-properties.json'
	& $go.Source run ./cmd/qqt-static-room-properties -file (Join-Path $targetClient 'QQTSection.dll') -out $roomPropertiesPatchReport
	if ($LASTEXITCODE -ne 0) { throw "QQTSection static room-properties patch exited with $LASTEXITCODE" }
	& $go.Source run ./cmd/qqt-static-room-properties -check -file (Join-Path $targetClient 'QQTSection.dll')
	if ($LASTEXITCODE -ne 0) { throw "QQTSection static room-properties verification exited with $LASTEXITCODE" }
	$roomPropertiesPatchEvidence = Get-Content -Raw -LiteralPath $roomPropertiesPatchReport | ConvertFrom-Json
	$roomPropertiesPatchEvidence.path = 'runtime/client-patched/QQTSection.dll'
	$roomPropertiesPatchEvidence | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $roomPropertiesPatchReport -Encoding utf8
	$startRejectionPatchReport = Join-Path $target 'runtime\patches\qqtsection-start-rejection-prompt.json'
	& $go.Source run ./cmd/qqt-static-start-rejection -file (Join-Path $targetClient 'QQTSection.dll') -out $startRejectionPatchReport
	if ($LASTEXITCODE -ne 0) { throw "QQTSection static start-rejection prompt patch exited with $LASTEXITCODE" }
	& $go.Source run ./cmd/qqt-static-start-rejection -check -file (Join-Path $targetClient 'QQTSection.dll')
	if ($LASTEXITCODE -ne 0) { throw "QQTSection static start-rejection prompt verification exited with $LASTEXITCODE" }
	$startRejectionPatchEvidence = Get-Content -Raw -LiteralPath $startRejectionPatchReport | ConvertFrom-Json
	$startRejectionPatchEvidence.path = 'runtime/client-patched/QQTSection.dll'
	$startRejectionPatchEvidence | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $startRejectionPatchReport -Encoding utf8
	$profilePersistencePatchReport = Join-Path $target 'runtime\patches\qqtmodules-profile-persistence.json'
	& $go.Source run ./cmd/qqt-static-profile-persistence -file (Join-Path $targetClient 'QQTModules.dll') -out $profilePersistencePatchReport
	if ($LASTEXITCODE -ne 0) { throw "QQTModules static profile-persistence patch exited with $LASTEXITCODE" }
	& $go.Source run ./cmd/qqt-static-profile-persistence -check -file (Join-Path $targetClient 'QQTModules.dll')
	if ($LASTEXITCODE -ne 0) { throw "QQTModules static profile-persistence verification exited with $LASTEXITCODE" }
	$profilePersistencePatchEvidence = Get-Content -Raw -LiteralPath $profilePersistencePatchReport | ConvertFrom-Json
	$profilePersistencePatchEvidence.path = 'runtime/client-patched/QQTModules.dll'
	$profilePersistencePatchEvidence | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $profilePersistencePatchReport -Encoding utf8
    & $go.Source run ./cmd/qqt-commodity-config -client-root $targetClient -out (Join-Path $targetClient 'config\Commodity.ini')
    if ($LASTEXITCODE -ne 0) { throw "Commodity.ini generation exited with $LASTEXITCODE" }
    $serverConfig = Get-Content -Raw -LiteralPath (Join-Path $workspace 'configs\server-directory-local-ui.json') | ConvertFrom-Json
    $shopServerID = [uint32]$serverConfig.directory_hall.shop_server_id
    if ($shopServerID -eq 0) { throw 'Server config has no directory_hall.shop_server_id.' }
    $shopPatchReport = Join-Path $target 'runtime\patches\qqtdir-shop-server.json'
    & $go.Source run ./cmd/qqt-static-shop-server -file (Join-Path $targetClient 'QQTDir.dll') -server-id $shopServerID -out $shopPatchReport
    if ($LASTEXITCODE -ne 0) { throw "QQTDir static shop-server patch exited with $LASTEXITCODE" }
    & $go.Source run ./cmd/qqt-static-shop-server -check -file (Join-Path $targetClient 'QQTDir.dll') -server-id $shopServerID
    if ($LASTEXITCODE -ne 0) { throw "QQTDir static shop-server verification exited with $LASTEXITCODE" }
    $shopPatchEvidence = Get-Content -Raw -LiteralPath $shopPatchReport | ConvertFrom-Json
    $shopPatchEvidence.path = 'runtime/client-patched/QQTDir.dll'
    $shopPatchEvidence | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $shopPatchReport -Encoding utf8
	$multiClientPatchReport = Join-Path $target 'runtime\patches\core-multi-client.json'
	& $go.Source run ./cmd/qqt-static-multi-client -file (Join-Path $targetClient 'Core.dll') -out $multiClientPatchReport
	if ($LASTEXITCODE -ne 0) { throw "Core.dll static multi-client patch exited with $LASTEXITCODE" }
	& $go.Source run ./cmd/qqt-static-multi-client -check -file (Join-Path $targetClient 'Core.dll')
	if ($LASTEXITCODE -ne 0) { throw "Core.dll static multi-client verification exited with $LASTEXITCODE" }
	$multiClientPatchEvidence = Get-Content -Raw -LiteralPath $multiClientPatchReport | ConvertFrom-Json
	$multiClientPatchEvidence.path = 'runtime/client-patched/Core.dll'
	$multiClientPatchEvidence | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $multiClientPatchReport -Encoding utf8
	$forgePatchReport = Join-Path $target 'runtime\patches\client-avatar-forge-boundary.json'
	& $go.Source run ./cmd/qqt-static-avatar-forge `
		-file (Join-Path $targetClient 'Client.exe') `
		-metadata (Join-Path $targetClient 'Client.tp-free.json') `
		-out $forgePatchReport
	if ($LASTEXITCODE -ne 0) { throw "Client.exe static avatar-forge boundary patch exited with $LASTEXITCODE" }
	& $go.Source run ./cmd/qqt-static-avatar-forge -check `
		-file (Join-Path $targetClient 'Client.exe') `
		-metadata (Join-Path $targetClient 'Client.tp-free.json')
	if ($LASTEXITCODE -ne 0) { throw "Client.exe static avatar-forge boundary verification exited with $LASTEXITCODE" }
	$forgePatchEvidence = Get-Content -Raw -LiteralPath $forgePatchReport | ConvertFrom-Json
	$forgePatchEvidence.path = 'runtime/client-patched/Client.exe'
	$forgePatchEvidence.metadata_path = 'runtime/client-patched/Client.tp-free.json'
	$forgePatchEvidence | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $forgePatchReport -Encoding utf8
	$platformEffectPatchReport = Join-Path $target 'runtime\patches\client-platform-effects.json'
	& $go.Source run ./cmd/qqt-static-platform-effects `
		-file (Join-Path $targetClient 'Client.exe') `
		-metadata (Join-Path $targetClient 'Client.tp-free.json') `
		-out $platformEffectPatchReport
	if ($LASTEXITCODE -ne 0) { throw "Client.exe static platform-effect patch exited with $LASTEXITCODE" }
	& $go.Source run ./cmd/qqt-static-platform-effects -check `
		-file (Join-Path $targetClient 'Client.exe') `
		-metadata (Join-Path $targetClient 'Client.tp-free.json')
	if ($LASTEXITCODE -ne 0) { throw "Client.exe static platform-effect verification exited with $LASTEXITCODE" }
	$platformEffectPatchEvidence = Get-Content -Raw -LiteralPath $platformEffectPatchReport | ConvertFrom-Json
	$platformEffectPatchEvidence.path = 'runtime/client-patched/Client.exe'
	$platformEffectPatchEvidence.metadata_path = 'runtime/client-patched/Client.tp-free.json'
	$platformEffectPatchEvidence | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $platformEffectPatchReport -Encoding utf8
	$frameRatePatchReport = Join-Path $target 'runtime\patches\client-frame-rate.json'
	& $go.Source run ./cmd/qqt-static-frame-rate `
		-file (Join-Path $targetClient 'Client.exe') `
		-metadata (Join-Path $targetClient 'Client.tp-free.json') `
		-out $frameRatePatchReport
	if ($LASTEXITCODE -ne 0) { throw "Client.exe static frame-rate patch exited with $LASTEXITCODE" }
	& $go.Source run ./cmd/qqt-static-frame-rate -check `
		-file (Join-Path $targetClient 'Client.exe') `
		-metadata (Join-Path $targetClient 'Client.tp-free.json')
	if ($LASTEXITCODE -ne 0) { throw "Client.exe static frame-rate verification exited with $LASTEXITCODE" }
	$frameRatePatchEvidence = Get-Content -Raw -LiteralPath $frameRatePatchReport | ConvertFrom-Json
	$frameRatePatchEvidence.path = 'runtime/client-patched/Client.exe'
	$frameRatePatchEvidence.metadata_path = 'runtime/client-patched/Client.tp-free.json'
	$frameRatePatchEvidence | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $frameRatePatchReport -Encoding utf8
    # Preserve the two source-verified TerSafe factory contracts with a
    # dependency-free 1.5 KiB DLL: QQTModules' legacy CreateObj(3) no-op and
    # Client.exe's CreateObj(9) safe-value vtable. The latter leaves the
    # wrapper's canonical plain value untouched while replacing only TP's
    # protected-shadow validation. The original TP implementation and driver
    # remain absent.
    & $go.Source run ./cmd/qqt-tp-compat-dll -out (Join-Path $targetClient 'TerSafe.dll')
    if ($LASTEXITCODE -ne 0) { throw "TP-free compatibility DLL generation exited with $LASTEXITCODE" }
    & $go.Source build -trimpath -o (Join-Path $target 'runtime\bin\qqt-launcher-core.exe') ./cmd/qqt-launcher-ctl
    if ($LASTEXITCODE -ne 0) { throw "QQTang launcher controller release build exited with $LASTEXITCODE" }
    $csc = 'C:\Windows\Microsoft.NET\Framework64\v4.0.30319\csc.exe'
    if (-not (Test-Path -LiteralPath $csc -PathType Leaf)) { throw "Windows .NET Framework C# compiler is missing: $csc" }
    & $csc /nologo /target:winexe /platform:x64 /optimize+ `
        "/out:$target\QQTang-Launcher.exe" `
        "/win32icon:$workspace\cmd\qqt-launcher-winforms\QQTang.ico" `
        "/win32manifest:$workspace\cmd\qqt-launcher-winforms\QQTang-Launcher.exe.manifest" `
        /reference:System.dll /reference:System.Core.dll /reference:System.Drawing.dll `
        /reference:System.Windows.Forms.dll /reference:System.Web.Extensions.dll `
        (Join-Path $workspace 'cmd\qqt-launcher-winforms\Program.cs')
    if ($LASTEXITCODE -ne 0) { throw "QQTang WinForms launcher release build exited with $LASTEXITCODE" }
	$previousWindowsCGOEnabled = $env:CGO_ENABLED
	$previousWindowsCC = $env:CC
	try {
		$env:CGO_ENABLED = '1'
		# CGO parses CC as a command; preserve compiler paths containing spaces.
		$env:CC = '"' + $gcc.Source + '"'
		& $go.Source build -tags onnxruntime -trimpath -o (Join-Path $target 'runtime\bin\qqt-server-local.exe') ./cmd/qqt-server
		if ($LASTEXITCODE -ne 0) { throw "qqt-server release build exited with $LASTEXITCODE" }
	}
	finally {
		$env:CGO_ENABLED = $previousWindowsCGOEnabled
		$env:CC = $previousWindowsCC
	}
	if (-not $WindowsOnly) {
	$linuxAMD64Server = Join-Path $target 'runtime\bin\qqt-server-linux-amd64'
	$linuxARM64Server = Join-Path $target 'runtime\bin\qqt-server-linux-arm64'
	$linuxAMD64CC = @(
		$env:QQTANG_LINUX_AMD64_CC
		'D:\code\software\SysGCC\bin\x86_64-linux-gnu-gcc.exe'
		(Get-Command x86_64-linux-gnu-gcc.exe -ErrorAction SilentlyContinue).Source
	) | Where-Object { $_ -and (Test-Path -LiteralPath $_ -PathType Leaf) } | Select-Object -First 1
	$linuxARM64CC = @(
		$env:QQTANG_LINUX_ARM64_CC
		'D:\code\software\mingw-w64-x86_64-aarch64-none-linux-gnu\bin\aarch64-none-linux-gnu-gcc.exe'
		(Get-Command aarch64-none-linux-gnu-gcc.exe -ErrorAction SilentlyContinue).Source
		(Get-Command aarch64-linux-gnu-gcc.exe -ErrorAction SilentlyContinue).Source
	) | Where-Object { $_ -and (Test-Path -LiteralPath $_ -PathType Leaf) } | Select-Object -First 1
	if (-not $linuxAMD64CC) { throw 'Linux AMD64 cross compiler is missing. Set QQTANG_LINUX_AMD64_CC.' }
	if (-not $linuxARM64CC) { throw 'Linux ARM64 cross compiler is missing. Set QQTANG_LINUX_ARM64_CC.' }
	$crossBuilds = @(
		@{ Architecture = 'amd64'; Compiler = $linuxAMD64CC; Output = $linuxAMD64Server }
		@{ Architecture = 'arm64'; Compiler = $linuxARM64CC; Output = $linuxARM64Server }
	)
	$previousCGOEnabled = $env:CGO_ENABLED
	$previousGOOS = $env:GOOS
	$previousGOARCH = $env:GOARCH
	$previousCC = $env:CC
	try {
		foreach ($crossBuild in $crossBuilds) {
			$env:GOOS = 'linux'
			$env:GOARCH = $crossBuild.Architecture
			$env:CGO_ENABLED = '1'
			$env:CC = $crossBuild.Compiler
			& $go.Source build -tags onnxruntime -trimpath -o $crossBuild.Output ./cmd/qqt-server
			if ($LASTEXITCODE -ne 0) {
				throw "Linux $($crossBuild.Architecture) ONNX Runtime server build exited with $LASTEXITCODE"
			}
		}
	}
	finally {
		$env:CGO_ENABLED = $previousCGOEnabled
		$env:GOOS = $previousGOOS
		$env:GOARCH = $previousGOARCH
		$env:CC = $previousCC
	}
    Assert-LinuxServerBinary (Join-Path $target 'runtime\bin\qqt-server-linux-amd64') 0x3E 'amd64'
    Assert-LinuxServerBinary (Join-Path $target 'runtime\bin\qqt-server-linux-arm64') 0xB7 'arm64'
	foreach ($linuxServer in @($linuxAMD64Server, $linuxARM64Server)) {
		$buildInfo = (& $go.Source version -m $linuxServer | Out-String)
		if ($buildInfo -notmatch 'CGO_ENABLED=1' -or
			$buildInfo -notmatch 'github.com/microsoft/onnxruntime/go') {
			throw "Linux server was built without the ONNX Runtime CGO backend: $linuxServer"
		}
	}
	}
    & $go.Source build -trimpath -o (Join-Path $target 'runtime\bin\qqt-launch-local.exe') ./cmd/qqt-launch
    if ($LASTEXITCODE -ne 0) { throw "qqt-launch release build exited with $LASTEXITCODE" }
    & $go.Source build -trimpath -o (Join-Path $target 'runtime\bin\qqt-login-immediate.exe') ./cmd/qqt-login-immediate
    if ($LASTEXITCODE -ne 0) { throw "qqt-login-immediate release build exited with $LASTEXITCODE" }
}
finally {
    Pop-Location
}

Get-ChildItem -LiteralPath (Join-Path $target 'runtime\client-patched') -Recurse -File |
    Where-Object {
        $_.Extension -in @('.log', '.dmp', '.tmp', '.tem') -and
        $_.Name -ne 'QQTLiveUpdateLogFile.log'
    } |
    Remove-Item -Force
Get-ChildItem -LiteralPath (Join-Path $target 'runtime\client-patched') -Recurse -Force -File |
    Where-Object { $_.Name -eq 'Thumbs.db' } |
    Remove-Item -Force
foreach ($relativePath in @('TenioLog', 'Record', 'profile', 'qqshow', 'runtime')) {
    $generatedDirectory = Join-Path $target "runtime\client-patched\$relativePath"
    if (Test-Path -LiteralPath $generatedDirectory) {
        Remove-Item -LiteralPath $generatedDirectory -Recurse -Force
    }
}
$clientDataDirectory = Join-Path $targetClient 'data'
if (Test-Path -LiteralPath $clientDataDirectory -PathType Container) {
    Get-ChildItem -LiteralPath $clientDataDirectory -File -Force |
        Where-Object { $_.Name -match '^\d+-(Friends|kininfo)\.dat$' } |
        Remove-Item -Force
}
# QQTDownloadTemp may contain real resource archives, but old endpoints also
# returned HTML error pages with a .zip suffix. Preserve only archives whose
# central directory can actually be opened; stale queue/error state must never
# become part of a release package.
$downloadCache = Join-Path $target 'runtime\client-patched\QQTDownloadTemp'
if (Test-Path -LiteralPath $downloadCache -PathType Container) {
    Get-ChildItem -LiteralPath $downloadCache -Recurse -Force -File |
        Where-Object {
            $_.Extension -in @('.tem', '.tmp', '.log', '.dmp') -or
            ($_.Extension -eq '.zip' -and -not (Test-ZipArchive $_.FullName))
        } |
        Remove-Item -Force
}
foreach ($relativePath in @('log.txt')) {
    Remove-Item -LiteralPath (Join-Path $target "runtime\client-patched\$relativePath") -Force -ErrorAction SilentlyContinue
}

$liveUpdateState = Join-Path $target 'runtime\client-patched\QQTLiveUpdateLogFile.log'
if (-not (Test-Path -LiteralPath $liveUpdateState -PathType Leaf)) {
    throw "Required final-client startup state is missing: $liveUpdateState"
}
if ((Get-Content -LiteralPath $liveUpdateState -Raw) -notmatch 'state\s*=\s*[01]') {
    throw "Required final-client startup state is invalid: $liveUpdateState"
}

# Runtime cleanup must never make the distributed client less complete than
# the verified original copy. Restore only baseline paths that cleanup removed;
# never overwrite files that remain in client-patched, because those may be
# deliberate compatibility/configuration changes.
$baselinePrefix = $baselineClient.TrimEnd([IO.Path]::DirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
$targetClient = Join-Path $target 'runtime\client-patched'
Get-ChildItem -LiteralPath $baselineClient -Recurse -Force -File | ForEach-Object {
    $relativePath = $_.FullName.Substring($baselinePrefix.Length)
    $topLevel = ($relativePath -split '[\\/]', 2)[0]
    if ($topLevel -in @('TenioLog', 'Record')) { return }
    if ($relativePath -eq 'README.md') { return }
    if ($_.Name -in @('ClientBase.dll', 'TerSafe.dll', 'TenSLX.dll', 'TP3Helper.exe', 'TesSafe.sys')) { return }
    if ((
            $_.Extension -in @('.log', '.dmp', '.tmp', '.tem') -and
            $_.Name -ne 'QQTLiveUpdateLogFile.log'
        ) -or $_.Name -eq 'Thumbs.db' -or $relativePath -eq 'log.txt') { return }
    $destination = Join-Path $targetClient $relativePath
    if (-not (Test-Path -LiteralPath $destination -PathType Leaf)) {
        New-Item -ItemType Directory -Force -Path (Split-Path -Parent $destination) | Out-Null
        Copy-Item -LiteralPath $_.FullName -Destination $destination -Force
    }
}

# Do not publish settings and machine state learned from the developer host.
# Resource/version files restored by the local project remain untouched, but
# display enumeration, launch counter, live download queue and dormant TP data
# return to the verified client baseline on every package build.
foreach ($relativePath in @('devConfig.txt', 'disp_ogl.txt', 'config\CacheConfig.xml', 'config\DLScript.xml', 'TenSLX.dat')) {
    Copy-Item -LiteralPath (Join-Path $baselineClient $relativePath) -Destination (Join-Path $targetClient $relativePath) -Force
}

$invalidReleaseArchives = @(
    Get-ChildItem -LiteralPath (Join-Path $targetClient 'QQTDownloadTemp') -Recurse -Force -File -Filter '*.zip' -ErrorAction SilentlyContinue |
        Where-Object { -not (Test-ZipArchive $_.FullName) }
)
if ($invalidReleaseArchives.Count -ne 0) {
    throw "Release safety invariant failed: invalid resource archives remain: $($invalidReleaseArchives.FullName -join ', ')"
}

foreach ($name in @('ClientBase.dll', 'TenSLX.dll', 'TP3Helper.exe', 'TesSafe.sys')) {
    if (Test-Path -LiteralPath (Join-Path $targetClient $name)) {
        throw "Release safety invariant failed: dormant TP component must not be distributed: $name"
    }
}
if (Test-Path -LiteralPath (Join-Path $target 'runtime\bin\qqt-shop-server-probe.exe')) {
    throw 'Release safety invariant failed: runtime shop-server injection helper must not be distributed.'
}
$tpCompatibilityPath = Join-Path $targetClient 'TerSafe.dll'
if (-not (Test-Path -LiteralPath $tpCompatibilityPath -PathType Leaf)) {
    throw 'Release safety invariant failed: TP-free TerSafe loader compatibility DLL is missing.'
}
$tpCompatibility = Get-Item -LiteralPath $tpCompatibilityPath
$tpCompatibilityDigest = (Get-FileHash -LiteralPath $tpCompatibilityPath -Algorithm SHA256).Hash.ToUpperInvariant()
if ($tpCompatibility.Length -ne 2048 -or $tpCompatibilityDigest -ne '1FFBE92E52862C8270E4FAF83AE6FC29AE986C8C638F6D5E295C76BCD1C07A9D') {
    throw "Release safety invariant failed: unexpected TerSafe compatibility DLL (size=$($tpCompatibility.Length), sha256=$tpCompatibilityDigest)."
}
$tpFreeMetadataPath = Join-Path $targetClient 'Client.tp-free.json'
$tpFreeMetadata = Get-Content -LiteralPath $tpFreeMetadataPath -Raw -Encoding UTF8 | ConvertFrom-Json
$clientDigest = (Get-FileHash -LiteralPath (Join-Path $targetClient 'Client.exe') -Algorithm SHA256).Hash.ToLowerInvariant()
if ($clientDigest -ne ([string]$tpFreeMetadata.sha256).ToLowerInvariant()) {
    throw 'Release safety invariant failed: Client.exe does not match Client.tp-free.json.'
}
if ([bool]$tpFreeMetadata.protection_imports_present) {
    throw 'Release safety invariant failed: reconstructed Client.exe still imports a protection module.'
}
$generatedState = [Collections.Generic.List[string]]::new()
foreach ($relativePath in @('runtime', 'qqshow', 'Record', 'TenioLog')) {
    $generatedDirectory = Join-Path $targetClient $relativePath
    if (Test-Path -LiteralPath $generatedDirectory -PathType Container) {
        foreach ($file in Get-ChildItem -LiteralPath $generatedDirectory -Recurse -Force -File) {
            [void]$generatedState.Add($file.FullName)
        }
    }
}
foreach ($file in Get-ChildItem -LiteralPath (Join-Path $targetClient 'data') -File -Force) {
    if ($file.Name -match '^\d+-(Friends|kininfo)\.dat$') { [void]$generatedState.Add($file.FullName) }
}
$releaseProfileFiles = @(Get-ChildItem -LiteralPath (Join-Path $targetClient 'profile') -Recurse -Force -File)
if ($releaseProfileFiles.Count -ne 1 -or $releaseProfileFiles[0].Name -ne '10000.tpf') {
    $profileNames = @($releaseProfileFiles | ForEach-Object { $_.FullName }) -join ', '
    throw "Release safety invariant failed: profile must contain only baseline 10000.tpf; found: $profileNames"
}
if ($generatedState.Count -ne 0) {
    throw "Release safety invariant failed: generated client account/machine state remains: $($generatedState -join ', ')"
}
foreach ($relativePath in @('devConfig.txt', 'disp_ogl.txt', 'config\CacheConfig.xml', 'config\DLScript.xml', 'TenSLX.dat', 'profile\10000.tpf')) {
    $baselineDigest = (Get-FileHash -LiteralPath (Join-Path $baselineClient $relativePath) -Algorithm SHA256).Hash
    $releaseDigest = (Get-FileHash -LiteralPath (Join-Path $targetClient $relativePath) -Algorithm SHA256).Hash
    if ($baselineDigest -ne $releaseDigest) {
        throw "Release safety invariant failed: developer machine state leaked through $relativePath"
    }
}
$shopPatchEvidence = Get-Content -Raw -LiteralPath (Join-Path $target 'runtime\patches\qqtdir-shop-server.json') | ConvertFrom-Json
if ($shopPatchEvidence.path -ne 'runtime/client-patched/QQTDir.dll' -or [uint32]$shopPatchEvidence.server_id -ne $shopServerID) {
    throw 'Release safety invariant failed: static shop patch evidence is not relocatable or has the wrong server ID.'
}
$multiClientPatchEvidence = Get-Content -Raw -LiteralPath (Join-Path $target 'runtime\patches\core-multi-client.json') | ConvertFrom-Json
if ($multiClientPatchEvidence.path -ne 'runtime/client-patched/Core.dll') {
	throw 'Release safety invariant failed: static multi-client patch evidence is not relocatable.'
}
$soloBossPatchEvidence = Get-Content -Raw -LiteralPath (Join-Path $target 'runtime\patches\qqtsection-solo-boss-card.json') | ConvertFrom-Json
if ($soloBossPatchEvidence.client_root -ne 'runtime/client-patched' -or
	$soloBossPatchEvidence.dll_path -ne 'runtime/client-patched/QQTSection.dll' -or
	[uint32]$soloBossPatchEvidence.item_id -ne 30098 -or
	[uint32]$soloBossPatchEvidence.resource_item_id -ne 99) {
	throw 'Release safety invariant failed: static single-player Boss-card patch evidence is incomplete or not relocatable.'
}
$roomPropertiesPatchEvidence = Get-Content -Raw -LiteralPath (Join-Path $target 'runtime\patches\qqtsection-room-properties.json') | ConvertFrom-Json
if ($roomPropertiesPatchEvidence.path -ne 'runtime/client-patched/QQTSection.dll' -or
	$roomPropertiesPatchEvidence.hook_rva -ne '0x29644' -or
	$roomPropertiesPatchEvidence.cave_rva -ne '0x53900' -or
	$roomPropertiesPatchEvidence.resume_rva -ne '0x2964A' -or
	[uint32]$roomPropertiesPatchEvidence.room_flag_request_offset -ne 32) {
	throw 'Release safety invariant failed: static room-properties patch evidence is incomplete or not relocatable.'
}
$profilePersistencePatchEvidence = Get-Content -Raw -LiteralPath (Join-Path $target 'runtime\patches\qqtmodules-profile-persistence.json') | ConvertFrom-Json
$profilePersistenceSiteNames = @($profilePersistencePatchEvidence.sites | ForEach-Object { $_.name })
if ($profilePersistencePatchEvidence.path -ne 'runtime/client-patched/QQTModules.dll' -or
	$profilePersistencePatchEvidence.cave_rva -ne '0x6B600' -or
	[uint32]$profilePersistencePatchEvidence.cave_size -ne 0x500 -or
	@($profilePersistencePatchEvidence.sites).Count -ne 16 -or
	'serializer-mode-entry' -notin $profilePersistenceSiteNames -or
	'destructor-serializer-call' -notin $profilePersistenceSiteNames -or
	'integer-setter-exit' -notin $profilePersistenceSiteNames -or
	'string-setter-exit' -notin $profilePersistenceSiteNames -or
	'buffer-setter-exit' -notin $profilePersistenceSiteNames -or
	'buffer2-setter-exit' -notin $profilePersistenceSiteNames) {
	throw 'Release safety invariant failed: static profile-persistence patch evidence is incomplete or not relocatable.'
}
$forgePatchEvidence = Get-Content -Raw -LiteralPath (Join-Path $target 'runtime\patches\client-avatar-forge-boundary.json') | ConvertFrom-Json
if ($forgePatchEvidence.path -ne 'runtime/client-patched/Client.exe' -or
	$forgePatchEvidence.metadata_path -ne 'runtime/client-patched/Client.tp-free.json' -or
	@($forgePatchEvidence.sites).Count -ne 2) {
	throw 'Release safety invariant failed: static avatar-forge boundary patch evidence is incomplete or not relocatable.'
}
$frameRatePatchEvidence = Get-Content -Raw -LiteralPath (Join-Path $target 'runtime\patches\client-frame-rate.json') | ConvertFrom-Json
$frameRatePatchSiteNames = @($frameRatePatchEvidence.sites | ForEach-Object { $_.name })
if ($frameRatePatchEvidence.path -ne 'runtime/client-patched/Client.exe' -or
	$frameRatePatchEvidence.metadata_path -ne 'runtime/client-patched/Client.tp-free.json' -or
	$frameRatePatchEvidence.section_rva -ne '0xD37000' -or
	[uint32]$frameRatePatchEvidence.section_size -le 0 -or
	@($frameRatePatchEvidence.sites).Count -ne 5 -or
	'legacy-render-throttle-compare' -notin $frameRatePatchSiteNames -or
	'legacy-render-throttle-target' -notin $frameRatePatchSiteNames -or
	'phantom-history-time-sampling' -notin $frameRatePatchSiteNames -or
	'banana-forced-slide-constructor' -notin $frameRatePatchSiteNames -or
	'banana-forced-slide-position-sampling' -notin $frameRatePatchSiteNames) {
	throw 'Release safety invariant failed: static frame-rate patch evidence is incomplete or not relocatable.'
}

foreach ($fileName in @('server-directory-local-ui.json', 'adventure-rules.json', 'role-rules.json', 'starter-loadouts.json', 'break-egg-rewards.json', 'item-image-aliases.json', 'network.json', 'sso-message-trace-stable.json')) {
    Copy-Item -LiteralPath (Join-Path $workspace "configs\$fileName") -Destination (Join-Path $target "configs\$fileName") -Force
}
if (-not $WindowsOnly) {
	foreach ($architecture in @('amd64', 'arm64')) {
		$linuxServerConfig = Get-Content -Raw -LiteralPath (Join-Path $target 'configs\server-directory-local-ui.json') | ConvertFrom-Json
		$linuxServerConfig.competitive_ai.backend = 'onnxruntime'
		$linuxServerConfig.competitive_ai.model_path = 'models/qqtang-rule1.onnx'
		$linuxServerConfig.competitive_ai | Add-Member -NotePropertyName metadata_path -NotePropertyValue 'models/qqtang-rule1.onnx.json' -Force
		$linuxServerConfig.competitive_ai.shared_library_path = ''
		$linuxServerConfig | ConvertTo-Json -Depth 20 | Set-Content -LiteralPath (Join-Path $target "configs\server-directory-local-ui-linux-$architecture.json") -Encoding utf8
	}
}
New-Item -ItemType Directory -Force -Path (Join-Path $target 'configs\models') | Out-Null
Copy-Item -LiteralPath (Join-Path $workspace 'configs\models\qqtang-rule1.onnx') -Destination (Join-Path $target 'configs\models\qqtang-rule1.onnx') -Force
Copy-Item -LiteralPath (Join-Path $workspace 'configs\models\qqtang-rule1.onnx.json') -Destination (Join-Path $target 'configs\models\qqtang-rule1.onnx.json') -Force
Copy-Item -LiteralPath (Join-Path $workspace 'configs\models\README.md') -Destination (Join-Path $target 'configs\models\README.md') -Force
New-Item -ItemType Directory -Force -Path (Join-Path $target 'runtime\onnxruntime\windows-amd64') | Out-Null
Copy-Item -LiteralPath (Join-Path $workspace 'build-assets\onnxruntime\windows-amd64\onnxruntime.dll') -Destination (Join-Path $target 'runtime\onnxruntime\windows-amd64\onnxruntime.dll') -Force
Copy-Item -LiteralPath (Join-Path $workspace 'build-assets\onnxruntime\windows-amd64\onnxruntime_providers_shared.dll') -Destination (Join-Path $target 'runtime\onnxruntime\windows-amd64\onnxruntime_providers_shared.dll') -Force
if (-not $WindowsOnly) {
	foreach ($architecture in @('amd64', 'arm64')) {
		$linuxRuntimeSource = Join-Path $workspace "build-assets\onnxruntime\linux-$architecture"
		$linuxRuntimeDestination = Join-Path $target "runtime\onnxruntime\linux-$architecture"
		New-Item -ItemType Directory -Force -Path $linuxRuntimeDestination | Out-Null
		Copy-Item -LiteralPath (Join-Path $linuxRuntimeSource 'libonnxruntime.so.1.29.0') -Destination (Join-Path $linuxRuntimeDestination 'libonnxruntime.so.1.29.0') -Force
		Copy-Item -LiteralPath (Join-Path $linuxRuntimeSource 'libonnxruntime_providers_shared.so') -Destination (Join-Path $linuxRuntimeDestination 'libonnxruntime_providers_shared.so') -Force
	}
}
Copy-Item -LiteralPath (Join-Path $workspace 'data\qqt_combine_recipes.json') -Destination (Join-Path $target 'data\qqt_combine_recipes.json') -Force
Copy-Item -LiteralPath (Join-Path $workspace 'data\qqt_forge_rules.json') -Destination (Join-Path $target 'data\qqt_forge_rules.json') -Force
Copy-Item -LiteralPath (Join-Path $workspace 'deploy\windows\README.txt') -Destination (Join-Path $target 'README.txt') -Force
Copy-Item -LiteralPath (Join-Path $workspace 'deploy\windows\start-server-windows.cmd') -Destination (Join-Path $target 'start-server-windows.cmd') -Force
if (-not $WindowsOnly) {
	Copy-Item -LiteralPath (Join-Path $workspace 'deploy\linux\start-server-linux-amd64.sh') -Destination (Join-Path $target 'start-server-linux-amd64.sh') -Force
	Copy-Item -LiteralPath (Join-Path $workspace 'deploy\linux\start-server-linux-arm64.sh') -Destination (Join-Path $target 'start-server-linux-arm64.sh') -Force
}

$manifestFiles = Get-ChildItem -LiteralPath $target -Recurse -File | Sort-Object FullName
$targetPrefix = $target.TrimEnd([IO.Path]::DirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
$manifest = [pscustomobject]@{
    schema_version = 1
    generated_utc = (Get-Date).ToUniversalTime().ToString('o')
    package_name = $packageName
    files = @($manifestFiles | ForEach-Object {
        [pscustomobject]@{
            path = $_.FullName.Substring($targetPrefix.Length).Replace('\', '/')
            size = $_.Length
            sha256 = (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
        }
    })
}
$manifestPath = Join-Path $target 'release-manifest.json'
$manifest | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $manifestPath -Encoding utf8
$writtenManifest = Get-Content -LiteralPath $manifestPath -Raw -Encoding UTF8 | ConvertFrom-Json
if ($writtenManifest.package_name -ne $packageName -or @($writtenManifest.files).Count -ne $manifestFiles.Count) {
    throw "Release manifest verification failed for package $packageName"
}
Copy-Item -LiteralPath (Join-Path $workspace 'deploy\windows\RELEASE.md') -Destination (Join-Path $releaseRoot 'README.md') -Force

$totalBytes = (Get-ChildItem -LiteralPath $target -Recurse -File | Measure-Object Length -Sum).Sum
$archivePath = Join-Path $releaseRoot ($packageName + '.zip')
# Keep only the current distributable archive. Historical versioned ZIPs made
# iterative testing confusing and consumed several gigabytes; the unpacked
# release directory remains the source for the newly generated fixed name.
Get-ChildItem -LiteralPath $releaseRoot -File -Filter ($packageName + '*.zip') -ErrorAction SilentlyContinue |
    Remove-Item -Force
Compress-Archive -LiteralPath $target -DestinationPath $archivePath -CompressionLevel Optimal -Force
if ($WindowsOnly) { Write-Host 'Windows-only release: Linux servers, ONNX runtimes and start scripts were skipped.' }
Write-Host "Release ready: $target"
Write-Host "Files: $((Get-ChildItem -LiteralPath $target -Recurse -File).Count); bytes: $totalBytes"
Write-Host "Archive: $archivePath"
