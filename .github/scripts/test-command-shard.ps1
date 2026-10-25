[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [int] $ShardIndex,
    [int] $ShardCount = 8,
    [ValidateSet('20m', '60m')]
    [string] $Timeout = '20m',
    [switch] $Race,
    [switch] $ListOnly
)

$ErrorActionPreference = 'Stop'
if ($ShardCount -le 0 -or $ShardIndex -lt 0 -or $ShardIndex -ge $ShardCount) {
    throw 'ShardIndex must be between zero and ShardCount minus one.'
}

$listArguments = @('test')
if ($Race) {
    $listArguments += '-race'
}
$listArguments += @('./internal/command', '-list', '^(Test|Fuzz|Example)')
$listing = @(& go @listArguments)
if ($LASTEXITCODE -ne 0) {
    throw "Command test listing failed with exit code $LASTEXITCODE."
}

$names = [System.Collections.Generic.List[string]]::new()
$seen = [System.Collections.Generic.HashSet[string]]::new([System.StringComparer]::Ordinal)
$packageListed = $false
foreach ($line in $listing) {
    if ($line -cmatch '^(Test|Fuzz|Example)[\p{L}\p{Nd}_]*$') {
        if (-not $seen.Add($line)) {
            throw "Duplicate command test name: $line"
        }
        $names.Add($line)
    } elseif ($line -cmatch '^ok[\t ]+github\.com/plystra/cli/internal/command([\t ]|$)' -or
              $line -cmatch '^\?[\t ]+github\.com/plystra/cli/internal/command[\t ]+\[no test files\]$') {
        $packageListed = $true
    } elseif ($line -ne '') {
        throw "Unexpected command test listing output: $line"
    }
}
if (-not $packageListed) {
    throw 'Go did not report the command package listing.'
}

[string[]] $ordered = $names.ToArray()
[System.Array]::Sort($ordered, [System.StringComparer]::Ordinal)
$selected = @(
    for ($index = 0; $index -lt $ordered.Count; $index++) {
        if ($index % $ShardCount -eq $ShardIndex) {
            $ordered[$index]
        }
    }
)
$pattern = $null
$testArguments = @()
if ($selected.Count -gt 0) {
    $escaped = @($selected | ForEach-Object { [regex]::Escape($_) })
    $pattern = '^(' + ($escaped -join '|') + ')$'
    $testArguments = @('test')
    if ($Race) {
        $testArguments += '-race'
    }
    $testArguments += @("-timeout=$Timeout", './internal/command', '-run', $pattern)
}

if ($ListOnly) {
    [pscustomobject]@{
        ShardIndex = $ShardIndex
        ShardCount = $ShardCount
        TotalCount = $ordered.Count
        Race = [bool] $Race
        Names = @($selected)
        Pattern = $pattern
        ListArguments = @($listArguments)
        TestArguments = @($testArguments)
    } | ConvertTo-Json -Depth 4
    return
}
if ($selected.Count -eq 0) {
    Write-Host "Command shard $ShardIndex/$ShardCount is empty; no tests invoked."
    return
}

Write-Host "Command shard $ShardIndex/$ShardCount selects $($selected.Count)/$($ordered.Count) top-level tests, fuzz seeds, and examples."
& go @testArguments
if ($LASTEXITCODE -ne 0) {
    throw "Command shard $ShardIndex failed with exit code $LASTEXITCODE."
}
