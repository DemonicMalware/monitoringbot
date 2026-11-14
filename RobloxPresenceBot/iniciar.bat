@echo off
chcp 65001 >nul
cd /d "%~dp0"
title Roblox Presence Monitor
node bot.js start
if errorlevel 1 (
  echo.
  echo El monitor se detuvo con un error. Revisa el mensaje de arriba.
  pause
)
