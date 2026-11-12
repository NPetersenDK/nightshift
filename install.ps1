# Installs the latest nightshift release.  Usage: irm https://raw.githubusercontent.com/NPetersenDK/nightshift/main/install.ps1 | iex
$ErrorActionPreference = 'Stop'
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
$dir = Join-Path $env:LOCALAPPDATA 'nightshift'
New-Item -ItemType Directory -Force $dir | Out-Null
$zip = Join-Path $env:TEMP 'nightshift.zip'
Invoke-WebRequest "https://github.com/NPetersenDK/nightshift/releases/latest/download/nightshift_windows_$arch.zip" -OutFile $zip
Expand-Archive $zip -DestinationPath $dir -Force
Remove-Item $zip
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if ($userPath -notlike "*$dir*") { [Environment]::SetEnvironmentVariable('Path', "$userPath;$dir", 'User') }
"installed $dir\nightshift.exe (open a new terminal)"
'next: mkdir ~\nightshift; cd ~\nightshift; nightshift init'
