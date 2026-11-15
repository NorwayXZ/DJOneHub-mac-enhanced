; DJOneHub for Windows installer (Inno Setup 6).
; iscc /DAppVersion=1.3.0-proxy-preview.64 /DBuildDir=..\dist\windows DJOneHub.iss
;
; Installs the DJOneHub service (modules, proxies, SMS, recovery; runs from
; boot without anyone signed in) and the DJOneHub tray app (window and
; notifications), like the macOS backend agent and menu bar app.

#ifndef AppVersion
  #define AppVersion "0.0.0"
#endif
#ifndef BuildDir
  #define BuildDir "..\dist\windows"
#endif

[Setup]
AppId={{6D4F2B1A-8C3E-4F7A-9B21-D30E5A774C01}
AppName=DJOneHub
AppVersion={#AppVersion}
AppVerName=DJOneHub {#AppVersion}
AppPublisher=DJOneHub
AppPublisherURL=https://github.com/NorwayXZ/DJOneHub-mac-enhanced
DefaultDirName={autopf}\DJOneHub
DefaultGroupName=DJOneHub
DisableProgramGroupPage=yes
DisableDirPage=auto
PrivilegesRequired=admin
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
MinVersion=10.0.17763
OutputDir={#BuildDir}
OutputBaseFilename=DJOneHub-Windows-Setup-v{#AppVersion}
SetupIconFile=DJOneHub.ico
UninstallDisplayIcon={app}\DJOneHub.ico
UninstallDisplayName=DJOneHub
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
CloseApplications=no
RestartApplications=no

[Languages]
Name: "zh"; MessagesFile: "ChineseSimplified.isl"

[Files]
Source: "{#BuildDir}\djonehub-service.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#BuildDir}\DJOneHub.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "DJOneHub.ico"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\LICENSE"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\THIRD_PARTY_NOTICES.md"; DestDir: "{app}"; Flags: ignoreversion

[InstallDelete]
; Test builds named the service binary djonehub.exe.
Type: files; Name: "{app}\djonehub.exe"

[Icons]
Name: "{group}\DJOneHub"; Filename: "{app}\DJOneHub.exe"; IconFilename: "{app}\DJOneHub.ico"
Name: "{group}\卸载 DJOneHub"; Filename: "{uninstallexe}"
Name: "{autodesktop}\DJOneHub"; Filename: "{app}\DJOneHub.exe"; IconFilename: "{app}\DJOneHub.ico"; Tasks: desktopicon
; The tray starts at sign-in for notifications; the service needs no sign-in.
Name: "{commonstartup}\DJOneHub"; Filename: "{app}\DJOneHub.exe"; Parameters: "--background"; IconFilename: "{app}\DJOneHub.ico"

[Tasks]
Name: "desktopicon"; Description: "在桌面创建快捷方式"; GroupDescription: "快捷方式："

[Run]
Filename: "{app}\djonehub-service.exe"; Parameters: "--install-service"; Flags: runhidden waituntilterminated; StatusMsg: "正在安装并启动 DJOneHub 后台服务…"
Filename: "{app}\DJOneHub.exe"; Description: "打开 DJOneHub"; Flags: nowait postinstall skipifsilent runasoriginaluser

[UninstallRun]
Filename: "{sys}\taskkill.exe"; Parameters: "/F /IM DJOneHub.exe"; Flags: runhidden; RunOnceId: "StopTray"
Filename: "{app}\djonehub-service.exe"; Parameters: "--uninstall-service"; Flags: runhidden waituntilterminated; RunOnceId: "RemoveService"

[Code]
// An upgrade replaces files the running service and tray hold open.
function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  Code: Integer;
begin
  Exec(ExpandConstant('{sys}\taskkill.exe'), '/F /IM DJOneHub.exe', '', SW_HIDE, ewWaitUntilTerminated, Code);
  Exec(ExpandConstant('{sys}\sc.exe'), 'stop DJOneHub', '', SW_HIDE, ewWaitUntilTerminated, Code);
  Sleep(2500);
  Exec(ExpandConstant('{sys}\taskkill.exe'), '/F /IM djonehub-service.exe', '', SW_HIDE, ewWaitUntilTerminated, Code);
  Exec(ExpandConstant('{sys}\taskkill.exe'), '/F /IM djonehub.exe', '', SW_HIDE, ewWaitUntilTerminated, Code);
  Result := '';
end;
