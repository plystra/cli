param([switch] $Enumerate)

$ErrorActionPreference = 'Stop'
$runner = Join-Path $PSScriptRoot 'test-command-shard.ps1'
$previousExitCode = $global:LASTEXITCODE
$global:CommandShardCheckState = @{}

# Intercept only this process's Go calls. No test package is executed here.
function global:go {
    $received = @($args | ForEach-Object { [string] $_ })
    $global:CommandShardCheckState.Calls.Add($received)
    if ($received -contains '-list') {
        $global:LASTEXITCODE = $global:CommandShardCheckState.ListExit
        $global:CommandShardCheckState.Listing
    } else {
        $global:LASTEXITCODE = $global:CommandShardCheckState.TestExit
    }
}

function Reset-Calls {
    $global:CommandShardCheckState = @{
        Calls = [System.Collections.Generic.List[object]]::new()
        Listing = @('TestZ', 'TestA', 'FuzzSeed', 'Example_sample', 'TestAB', 'ok github.com/plystra/cli/internal/command 0.001s')
        ListExit = 0
        TestExit = 0
    }
}

function Assert-Check([bool] $Condition, [string] $Message) {
    if (-not $Condition) { throw $Message }
}

function Assert-Fails([scriptblock] $Action, [string] $Message) {
    $failed = $false
    try { & $Action | Out-Null } catch { $failed = $true }
    Assert-Check $failed $Message
}

try {
    Reset-Calls
    $plan = & $runner -ShardIndex 1 -ShardCount 3 -Race -ListOnly | ConvertFrom-Json
    Assert-Check (($plan.Names -join ',') -ceq 'FuzzSeed,TestZ') 'Ordinal partition lost a seed or test.'
    Assert-Check ($global:CommandShardCheckState.Calls.Count -eq 1) 'ListOnly executed tests.'
    Assert-Check ($plan.ListArguments -contains '-race') 'Race discovery omitted instrumentation.'
    Assert-Check ($plan.TestArguments -contains '-race') 'Race execution omitted instrumentation.'
    Assert-Check ($plan.TestArguments -contains '-timeout=20m') 'Linux race budget changed.'
    Assert-Check ('TestZ' -cmatch $plan.Pattern -and 'TestZExtra' -cnotmatch $plan.Pattern) 'Run pattern is not anchored.'
    Assert-Check (-not $plan.Pattern.Contains('/')) 'Run pattern narrowed subcases.'

    $union = [System.Collections.Generic.HashSet[string]]::new([System.StringComparer]::Ordinal)
    for ($shard = 0; $shard -lt 8; $shard++) {
        $part = & $runner -ShardIndex $shard -ListOnly | ConvertFrom-Json
        foreach ($name in $part.Names) {
            Assert-Check ($union.Add($name)) 'Shards overlap.'
            Assert-Check ($name -cmatch $part.Pattern) 'Selected name does not match its run pattern.'
        }
    }
    Assert-Check ($union.SetEquals([string[]] @('TestZ', 'TestA', 'FuzzSeed', 'Example_sample', 'TestAB'))) 'Partition omitted a test, fuzz seed, or example.'

    Reset-Calls
    & $runner -ShardIndex 0 -ShardCount 1 -Timeout 60m
    Assert-Check ($global:CommandShardCheckState.Calls.Count -eq 2) 'Nonempty shard did not invoke tests once.'
    Assert-Check ($global:CommandShardCheckState.Calls[1] -contains '-timeout=60m') 'Windows budget changed.'
    Assert-Check (-not ($global:CommandShardCheckState.Calls[0] -contains '-race')) 'Normal discovery added race.'
    Assert-Check (-not ($global:CommandShardCheckState.Calls[1] -contains '-race')) 'Normal execution added race.'

    Reset-Calls
    & $runner -ShardIndex 7 -ShardCount 8
    Assert-Check ($global:CommandShardCheckState.Calls.Count -eq 1) 'Empty shard accidentally ran all tests.'
    $empty = & $runner -ShardIndex 7 -ShardCount 8 -ListOnly | ConvertFrom-Json
    Assert-Check ($empty.Names.Count -eq 0 -and $empty.TestArguments.Count -eq 0 -and $null -eq $empty.Pattern) 'Empty shard produced an executable pattern.'

    foreach ($invalid in @(@(-1, 8), @(8, 8), @(0, 0))) {
        Reset-Calls
        Assert-Fails { & $runner -ShardIndex $invalid[0] -ShardCount $invalid[1] } 'Invalid index was accepted.'
        Assert-Check ($global:CommandShardCheckState.Calls.Count -eq 0) 'Invalid index reached Go.'
    }

    Reset-Calls
    $global:CommandShardCheckState.ListExit = 7
    Assert-Fails { & $runner -ShardIndex 0 } 'Listing failure was swallowed.'
    Assert-Check ($global:CommandShardCheckState.Calls.Count -eq 1) 'Failed listing started tests.'

    foreach ($invalidListing in @(@('TestA', 'unexpected output'), @('TestA', 'TestA', 'ok github.com/plystra/cli/internal/command 0.001s'))) {
        Reset-Calls
        $global:CommandShardCheckState.Listing = $invalidListing
        Assert-Fails { & $runner -ShardIndex 0 } 'Invalid listing was accepted.'
        Assert-Check ($global:CommandShardCheckState.Calls.Count -eq 1) 'Invalid listing started tests.'
    }

    Reset-Calls
    $global:CommandShardCheckState.TestExit = 9
    Assert-Fails { & $runner -ShardIndex 0 -ShardCount 1 -Race } 'Test failure was swallowed.'
    Assert-Check ($global:CommandShardCheckState.Calls.Count -eq 2) 'Failure propagation retried tests.'
    Assert-Check ($global:CommandShardCheckState.Calls[0] -contains '-race' -and $global:CommandShardCheckState.Calls[1] -contains '-race') 'Race plan was not used for actual Go calls.'
    Write-Host 'Command shard semantic checks passed (Go calls intercepted).'
} finally {
    Remove-Item Function:go
    Remove-Variable CommandShardCheckState -Scope Global
    $global:LASTEXITCODE = if ($null -eq $previousExitCode) { 0 } else { $previousExitCode }
}

if ($Enumerate) {
    foreach ($raceMode in @($false, $true)) {
        $arguments = @('test')
        if ($raceMode) { $arguments += '-race' }
        $arguments += @('./internal/command', '-list', '.')
        $catalog = @(& go @arguments)
        Assert-Check ($LASTEXITCODE -eq 0) 'Independent Go catalog listing failed.'
        [string[]] $expected = @($catalog | Where-Object { $_ -cmatch '^(Test|Fuzz|Example)' })
        $union = [System.Collections.Generic.HashSet[string]]::new([System.StringComparer]::Ordinal)
        $counts = @()
        for ($shard = 0; $shard -lt 8; $shard++) {
            $plan = & $runner -ShardIndex $shard -Race:$raceMode -ListOnly | ConvertFrom-Json
            Assert-Check ($plan.TotalCount -eq $expected.Count) 'Runner catalog differs from independent Go listing.'
            Assert-Check (($plan.ListArguments -contains '-race') -eq $raceMode) 'Listing mode changed.'
            Assert-Check (($plan.TestArguments -contains '-race') -eq ($raceMode -and $plan.Names.Count -gt 0)) 'Execution mode changed.'
            foreach ($name in $plan.Names) {
                Assert-Check ($union.Add($name)) 'Real command shards overlap.'
            }
            foreach ($name in $expected) {
                Assert-Check (($name -cmatch $plan.Pattern) -eq ($plan.Names -ccontains $name)) 'Run pattern does not exactly match membership.'
            }
            Assert-Check (-not $plan.Pattern.Contains('/')) 'Run pattern excludes subcases.'
            $counts += $plan.Names.Count
        }
        Assert-Check ($union.SetEquals($expected)) 'Real command partition omitted a test, seed, or example.'
        Write-Host "Compilation/list only: race=$raceMode total=$($expected.Count) shards=$($counts -join ',') union/disjoint/patterns passed."
    }
}
