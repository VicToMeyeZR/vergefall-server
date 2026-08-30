$auth = Invoke-RestMethod -Method Post -Uri "http://localhost:7350/v2/account/authenticate/device?create=true" -Headers @{Authorization="Basic ZGVmYXVsdGtleTo="} -ContentType "application/json" -Body '{"id":"dev-device-0000000001"}'
$TOKEN = $auth.token
$reports = Invoke-RestMethod -Method Post -Uri "http://localhost:7350/v2/rpc/vergefall.battle_reports" -Headers @{Authorization="Bearer $TOKEN"} -ContentType "application/json" -Body '"{}"'
$reports.payload | ConvertFrom-Json | ConvertTo-Json -Depth 10
