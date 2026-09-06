#Requires -RunAsAdministrator
<#
.SYNOPSIS
  Installs the SA05 desktop client and its privileged helper service.

.DESCRIPTION
  Copies sa05.exe, sa05ctl.exe, sa05-helper.exe and wintun.dll next to this
  script's layout into Program Files, then registers the sa05-helper Windows
  service (automatic start) via sa05-helper.exe --install-service.

  Run once from an elevated PowerShell:
    powershell -ExecutionPolicy Bypass -File install-windows.ps1

  Uninstall:
    powershell -ExecutionPolicy Bypass -File install-windows.ps1 -Uninstall
#>
param([switch]$Uninstall)

$ErrorActionPreference = 'Stop'

function Test-Admin {
  $id = [Security.Principal.WindowsIdentity]::GetCurrent()
  $principal = New-Object Security.Principal.WindowsPrincipal($id)
  return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

if (-not (Test-Admin)) {
  Write-Error 'Запустите от имени администратора: правый клик PowerShell -> "Запуск от имени администратора".'
  exit 1
}

$InstallDir = Join-Path ${env:ProgramFiles} 'SA05'
$Binaries = @('sa05.exe', 'sa05ctl.exe', 'sa05-helper.exe', 'wintun.dll')

# Two layouts: an unpacked release zip (binaries next to this script) or a
# source checkout (binaries in build/out).
$Here = Split-Path -Parent $MyInvocation.MyCommand.Path
$Out = $Here
if (-not (Test-Path (Join-Path $Here 'sa05-helper.exe'))) {
  $Out = Join-Path (Split-Path -Parent $Here) 'out'
  if ($Here -notlike '*build*') {
    $Out = Join-Path $Here 'build\out'
  }
}

if ($Uninstall) {
  Write-Host '>> Останавливаю и удаляю службу'
  $helper = Join-Path $InstallDir 'sa05-helper.exe'
  if (Test-Path $helper) {
    & $helper --uninstall-service 2>$null
  }
  if (Test-Path $InstallDir) {
    Remove-Item -Recurse -Force $InstallDir
  }
  Write-Host '>> Удалено.'
  exit 0
}

foreach ($binary in $Binaries) {
  if (-not (Test-Path (Join-Path $Out $binary))) {
    if ($binary -eq 'wintun.dll') {
      Write-Warning 'wintun.dll не найден: скачайте https://www.wintun.net/builds/wintun-0.14.1.zip и положите wintun/bin/amd64/wintun.dll рядом с sa05-helper.exe, иначе TUN не поднимется.'
      continue
    }
    Write-Error "Не найден $(Join-Path $Out $binary) — сначала соберите Windows-билд."
    exit 1
  }
}

Write-Host ">> Устанавливаю в $InstallDir"
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
foreach ($binary in $Binaries) {
  $source = Join-Path $Out $binary
  if (Test-Path $source) {
    Copy-Item -Force $source (Join-Path $InstallDir $binary)
  }
}

$assets = Join-Path $Out 'assets'
if (Test-Path $assets) {
  Write-Host '>> Устанавливаю гео-базы'
  Copy-Item -Recurse -Force $assets (Join-Path $InstallDir 'assets')
} else {
  Write-Host '>> Гео-базы не найдены (нужны профилям с geosite:): build/fetch-geoassets.sh'
}

Write-Host '>> Регистрирую службу sa05-helper'
& (Join-Path $InstallDir 'sa05-helper.exe') --install-service
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host '>> Готово. Запуск клиента: "$InstallDir\sa05.exe"'
Write-Host '>> Удаление: install-windows.ps1 -Uninstall'
