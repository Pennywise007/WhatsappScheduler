# build-windows.ps1
$env:CGO_ENABLED = "1"
$env:GOOS = "windows"

go build -o whatsapp-scheduler.exe .

if ($LASTEXITCODE -ne 0) {
    Write-Error "Build failed with exit code $LASTEXITCODE"
    exit $LASTEXITCODE
} else {
    Write-Output "Build succeeded: whatsapp-scheduler.exe created successfully."
}
