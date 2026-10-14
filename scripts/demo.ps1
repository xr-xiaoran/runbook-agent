$ErrorActionPreference='Stop'
if (!$env:API_KEY -or !$env:APPROVAL_KEY) { throw 'Set API_KEY and APPROVAL_KEY first.' }
$base='http://127.0.0.1:8084'
$health=Invoke-RestMethod "$base/health"
if ($health.mode -ne 'demo-scripted-no-llm') { throw 'This free demo script only runs in demo mode.' }
$headers=@{Authorization="Bearer $env:API_KEY"}
$body=@{question='CampusReward pending worker DB_PATH'} | ConvertTo-Json
$run=Invoke-RestMethod "$base/api/runs" -Method Post -Headers $headers -ContentType 'application/json; charset=utf-8' -Body $body
if ($run.status -ne 'completed' -or !$run.answer.draft) { throw 'Expected a completed run with a draft.' }
$approval=@{Authorization="Bearer $env:APPROVAL_KEY"}
$first=Invoke-RestMethod "$base/api/runs/$($run.id)/approve" -Method Post -Headers $approval -ContentType 'application/json' -Body '{"confirm":true}'
$again=Invoke-RestMethod "$base/api/runs/$($run.id)/approve" -Method Post -Headers $approval -ContentType 'application/json' -Body '{"confirm":true}'
if ($first.id -ne $again.id) { throw 'Approval was not idempotent.' }
[pscustomobject]@{mode=$run.mode;run_id=$run.id;status=$run.status;citations=$run.answer.citations;ticket_id=$first.id;repeat_ticket_id=$again.id} | ConvertTo-Json -Depth 6
