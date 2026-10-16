; tidebot desktop installer (per-user, no admin rights needed).
; makensis -DVERSION=0.1.0 -DSRC=path\to\dir -DOUTFILE=out.exe installer.nsi
Unicode true
!include "MUI2.nsh"
Name "tidebot"
OutFile "${OUTFILE}"
InstallDir "$LOCALAPPDATA\Programs\tidebot"
RequestExecutionLevel user
!define MUI_ICON "tidebot.ico"
!define MUI_UNICON "tidebot.ico"
VIProductVersion "${VERSION}.0"
VIAddVersionKey "ProductName" "tidebot"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "ProductVersion" "${VERSION}"
VIAddVersionKey "FileDescription" "tidebot installer"
VIAddVersionKey "LegalCopyright" "Zhengmingming1"
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"
!insertmacro MUI_LANGUAGE "SimpChinese"

Section "Install"
  SetOutPath "$INSTDIR"
  File "${SRC}\tidebot-desktop.exe"
  File "${SRC}\agentctl.exe"
  File "tidebot.ico"
  WriteUninstaller "$INSTDIR\uninstall.exe"
  CreateDirectory "$SMPROGRAMS\tidebot"
  CreateShortcut "$SMPROGRAMS\tidebot\tidebot.lnk" "$INSTDIR\tidebot-desktop.exe" "" "$INSTDIR\tidebot.ico"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\tidebot" "DisplayName" "tidebot"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\tidebot" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\tidebot" "Publisher" "Zhengmingming1"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\tidebot" "UninstallString" "$\"$INSTDIR\uninstall.exe$\""
  WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\tidebot" "NoModify" 1
SectionEnd

Section "Uninstall"
  Delete "$INSTDIR\tidebot-desktop.exe"
  Delete "$INSTDIR\agentctl.exe"
  Delete "$INSTDIR\tidebot.ico"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"
  Delete "$SMPROGRAMS\tidebot\tidebot.lnk"
  RMDir "$SMPROGRAMS\tidebot"
  DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "tidebot"
  DeleteRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\tidebot"
  ; your data in %USERPROFILE%\.tidebot is left alone on purpose
SectionEnd
