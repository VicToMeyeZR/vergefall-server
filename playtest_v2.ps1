# Vergefall M1 playtest v2 — full loop with automatic re-auth on token expiry.
# Usage:  .\playtest.ps1   (from the vergefall-server folder, server running)

$ErrorActionPreference = "Stop"
$base = "http://localhost:7350"
$script:TOKEN = $null

function Get-Token {
    $auth = Invoke-RestMethod -Method Post -Uri "$base/v2/account/authenticate/device?create=true" -Headers @{Authorization = "Basic ZGVmYXVsdGtleTo="} -ContentType "application/json" -Body '{"id":"dev-device-0000000001"}'
    $script:TOKEN = $auth.token
}

function Invoke-Rpc($name, $obj) {
    $inner = if ($null -eq $obj) { "{}" } else { $obj | ConvertTo-Json -Compress }
    $body = $inner | ConvertTo-Json   # Nakama wants string-wrapped JSON
    if ($null -eq $script:TOKEN) { Get-Token }
    try {
        $r = Invoke-RestMethod -Method Post -Uri "$base/v2/rpc/$name" -Headers @{Authorization = "Bearer $($script:TOKEN)"} -ContentType "application/json" -Body $body
    } catch {
        # Token expired (or any auth failure): refresh once and retry.
        Get-Token
        $r = Invoke-RestMethod -Method Post -Uri "$base/v2/rpc/$name" -Headers @{Authorization = "Bearer $($script:TOKEN)"} -ContentType "application/json" -Body $body
    }
    return ($r.payload | ConvertFrom-Json)
}

Write-Host "=== VERGEFALL M1 PLAYTEST ===" -ForegroundColor Cyan

Get-Token
Write-Host "[1/6] Authenticated." -ForegroundColor Green

$enlist = Invoke-Rpc "vergefall.enlist" $null
$fleetId = $enlist.fleet_id
Write-Host "[2/6] Enlisted. Fleet: $fleetId" -ForegroundColor Green

# If the fleet is already busy (e.g. rerun mid-flight), skip ordering.
$fv = Invoke-Rpc "vergefall.get_fleet" @{fleet_id = $fleetId}
if (-not $fv.busy) {
    $move = Invoke-Rpc "vergefall.fleet_order" @{fleet_id = $fleetId; kind = "move"; q = 4; r = -2}
    $arrive = [datetime]$move.arrive_at
    Write-Host "[3/6] Fleet underway. Arrives $($arrive.ToLocalTime().ToString('HH:mm:ss')) local." -ForegroundColor Green
} else {
    Write-Host "[3/6] Fleet already on an order (arrives $([datetime]$fv.arrive_at)). Waiting on it." -ForegroundColor Yellow
}

Write-Host "[4/6] Waiting for battle..." -NoNewline
$report = $null
while ($null -eq $report) {
    Start-Sleep -Seconds 10
    Write-Host "." -NoNewline
    $reps = Invoke-Rpc "vergefall.battle_reports" $null
    if ($reps -and $reps.Count -gt 0) { $report = $reps[-1] }
}
Write-Host ""
Write-Host "    BATTLE REPORT: $($report.narrative)" -ForegroundColor Yellow
$mine = $report.attacker
Write-Host ("    Your side: won={0} routed={1} casualties={2:P0}" -f $mine.won, $mine.routed, $mine.casualty_rate)

$view = Invoke-Rpc "vergefall.system_view" $null
$fleet = $view.fleets | Where-Object { $_.id -eq $fleetId }
$wreck = $view.wrecks | Where-Object { $_.hex.Q -eq $fleet.pos.Q -and $_.hex.R -eq $fleet.pos.R } | Select-Object -First 1
if ($null -eq $wreck) {
    Write-Host "[5/6] No wreck on our hex (routed and withdrew?). Fleet at ($($fleet.pos.Q),$($fleet.pos.R)); wrecks visible: $($view.wrecks.Count)." -ForegroundColor Yellow
    Write-Host "      Loop verified up to salvage." -ForegroundColor Yellow
    exit 0
}
Write-Host "[5/6] Wreck field $($wreck.id) on our hex. Salvaging (5 min op)..." -ForegroundColor Green
$null = Invoke-Rpc "vergefall.fleet_order" @{fleet_id = $fleetId; kind = "salvage"; wreck_id = $wreck.id}

Write-Host "[6/6] Waiting for salvage..." -NoNewline
do {
    Start-Sleep -Seconds 15
    Write-Host "." -NoNewline
    $fv = Invoke-Rpc "vergefall.get_fleet" @{fleet_id = $fleetId}
} while ($fv.busy)
Write-Host ""
Write-Host ("    SALVAGE HAUL: {0} Hullsteel, {1} Components" -f $fv.cargo[0], $fv.cargo[2]) -ForegroundColor Yellow
Write-Host ""
Write-Host "=== FULL LOOP COMPLETE: fight -> report -> wreck -> salvage -> cargo ===" -ForegroundColor Cyan
