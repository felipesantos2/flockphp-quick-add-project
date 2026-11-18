; Flock-Setup.exe: installs the tray app with the flock CLI next to it, for the current user only
; (no administrator rights). The app sets Flock up on its first start (`flock install`, one UAC
; prompt for the hosts helper and the CA). Built by windows\build-installer.ps1, which passes
; AppVersion, Arch (x64) and GoArch (amd64). Only x64 is built for now; x64compatible lets it
; install on Windows on ARM too, where it runs under emulation.

#ifndef AppVersion
  #define AppVersion "dev"
#endif
#ifndef Arch
  #define Arch "x64"
#endif
#ifndef GoArch
  #define GoArch "amd64"
#endif
; The Czech and Spanish messages below are UTF-8 without a BOM, which Inno Setup reads since 6.3.
#if Ver < EncodeVer(6, 3, 0)
  #error Inno Setup 6.3 or newer is needed to build Flock-Setup.exe.
#endif

[Setup]
AppId={{BD44E167-D13B-4AD9-8DA7-E976DEFCFB50}
AppName=Flock
AppVersion={#AppVersion}
AppPublisher=Guava s.r.o.
DefaultDirName={localappdata}\Programs\Flock
DisableDirPage=yes
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
OutputDir=..\dist
OutputBaseFilename=Flock-Setup-{#Arch}
SetupIconFile=..\Flock.Tray\Assets\AppIcon.ico
UninstallDisplayIcon={app}\FlockTray.exe
UninstallDisplayName=Flock
WizardStyle=modern
Compression=lzma2/max
SolidCompression=yes
; The tray app is stopped in PrepareToInstall; the Restart Manager would ask the user instead.
CloseApplications=no
; Tells Explorer to refresh its icon cache, or the Start menu and taskbar keep an updated app's old icon.
ChangesAssociations=yes
; Setup speaks the Windows display language when it is one of [Languages], else English (the first).
; The app's updater passes /LANG with the app's own language, so setup's errors match the app's.
LanguageDetectionMethod=uilanguage
ShowLanguageDialog=no
#ifdef Sign
; The "flock" sign tool build-installer.ps1 defines (windows\sign.ps1); signs the uninstaller too.
SignTool=flock
#endif

; Setup's languages ([Languages], English first) and its own messages in each, generated from lang/ (tools/i18n).
#include "Strings.iss"

[Files]
Source: "..\dist\FlockTray-{#Arch}\*"; DestDir: "{app}"; Flags: ignoreversion recursesubdirs createallsubdirs
Source: "..\..\dist\windows-{#GoArch}\flock.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\dist\windows-{#GoArch}\flock-helper.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\dist\windows-{#GoArch}\flockd.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\LICENSE"; DestDir: "{app}"; DestName: "LICENSE.txt"; Flags: ignoreversion
Source: "..\..\NOTICE"; DestDir: "{app}"; DestName: "NOTICE.txt"; Flags: ignoreversion
Source: "..\..\THIRD_PARTY_NOTICES"; DestDir: "{app}"; DestName: "THIRD_PARTY_NOTICES.txt"; Flags: ignoreversion

[InstallDelete]
; Tray icons of versions before the brand logomark (now Tray*.Light/Dark.ico).
Type: files; Name: "{app}\Assets\Flock.ico"
Type: files; Name: "{app}\Assets\FlockStopped.ico"
Type: files; Name: "{app}\Assets\FlockWarning.ico"

[Icons]
Name: "{userprograms}\Flock"; Filename: "{app}\FlockTray.exe"

[Registry]
; flock://install/<service>/<version> links from the website; the app asks before installing.
Root: HKCU; Subkey: "Software\Classes\flock"; ValueType: string; ValueName: ""; ValueData: "URL:Flock"; Flags: uninsdeletekey
Root: HKCU; Subkey: "Software\Classes\flock"; ValueType: string; ValueName: "URL Protocol"; ValueData: ""
Root: HKCU; Subkey: "Software\Classes\flock\DefaultIcon"; ValueType: string; ValueName: ""; ValueData: """{app}\FlockTray.exe"",0"
Root: HKCU; Subkey: "Software\Classes\flock\shell\open\command"; ValueType: string; ValueName: ""; ValueData: """{app}\FlockTray.exe"" ""%1"""

[Run]
Filename: "{app}\FlockTray.exe"; Description: "{cm:Installer_OpenFlock}"; Flags: postinstall nowait skipifsilent
; The app updates itself by running this installer silently with /RELAUNCH; it starts again quietly.
Filename: "{app}\FlockTray.exe"; Parameters: "--background"; Flags: nowait; Check: HasParam('/RELAUNCH')

[Code]
// /PURGE: the app's uninstall dialog, when the user also wants their data deleted.
// /SKIPFLOCK: `flock uninstall` started this uninstaller after removing Flock itself.
// /RELAUNCH: the app's updater runs this installer (silently) and wants the app back afterwards.
// /RESULT=<file>: the app's updater reads from there why setup stopped before installing.
function HasParam(Name: String): Boolean;
var
  I: Integer;
begin
  Result := False;
  for I := 1 to ParamCount do
    if CompareText(ParamStr(I), Name) = 0 then
      Result := True;
end;

// Over a set-up Flock, the new flock.exe first updates what needs administrator rights: the DNS
// rule, the hosts helper and the CA's trust (Windows asks only when one of them changed). When the
// user declines, setup stops before replacing anything and the installed version stays as it was.
function UpdatePrivileged(): String;
var
  ResultCode, I: Integer;
  Lines: TArrayOfString;
  Flock, Output: String;
begin
  Result := '';
  if not FileExists(ExpandConstant('{userappdata}\Flock\bin\flock.exe')) then
    Exit;
  ExtractTemporaryFile('flock.exe');
  ExtractTemporaryFile('flock-helper.exe');
  Flock := ExpandConstant('{tmp}\flock.exe');
  Output := ExpandConstant('{tmp}\privileged.txt');
  if Exec(ExpandConstant('{cmd}'), '/S /C ""' + Flock + '" install --privileged-only > "' + Output + '" 2>&1"',
          '', SW_HIDE, ewWaitUntilTerminated, ResultCode) and (ResultCode = 0) then
    Exit;

  Result := CustomMessage('Installer_PrivilegedFailed');
  if LoadStringsFromFile(Output, Lines) then
    for I := 0 to GetArrayLength(Lines) - 1 do
      if Pos('Error: ', Lines[I]) = 1 then
        Result := Copy(Lines[I], 8, Length(Lines[I]));
  if Pos('declined', Result) > 0 then
    Result := CustomMessage('Installer_PrivilegedDeclined')
  else
    Result := FmtMessage(CustomMessage('Installer_UpdateNotInstalled'), [Result]);
end;

// The running tray app locks its files; an update replaces them, and the new app starts afterwards.
function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  ResultCode: Integer;
  Lines: TArrayOfString;
begin
  Result := UpdatePrivileged();
  if Result <> '' then
  begin
    // UTF-8, as the app reads it: the message may be Czech or Spanish (SaveStringToFile writes ANSI).
    SetArrayLength(Lines, 1);
    Lines[0] := Result;
    if ExpandConstant('{param:RESULT|}') <> '' then
      SaveStringsToUTF8File(ExpandConstant('{param:RESULT|}'), Lines, False);
    // The app's updater waits for setup, except in versions before this check, which quit already.
    if HasParam('/RELAUNCH') and not CheckForMutexes('Local\Flock.Tray') then
      Exec(ExpandConstant('{app}\FlockTray.exe'), '--background', '', SW_SHOW, ewNoWait, ResultCode);
    Exit;
  end;

  Exec(ExpandConstant('{sys}\taskkill.exe'), '/F /IM FlockTray.exe', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  if ResultCode = 0 then
    Sleep(500);
end;

// Runs at uninstall time, before the app's files go: `flock uninstall` removes the daemon, PATH
// entry, hosts helper and CA trust (UAC prompt) and Flock's files, keeping the user's settings and
// databases unless /PURGE. Skipped when Flock was never set up (it would ask for permission).
procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  ResultCode: Integer;
  Params: String;
begin
  if CurUninstallStep = usUninstall then
  begin
    Exec(ExpandConstant('{sys}\taskkill.exe'), '/F /IM FlockTray.exe', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
    if not HasParam('/SKIPFLOCK') and FileExists(ExpandConstant('{userappdata}\Flock\bin\flock.exe')) then
    begin
      Params := 'uninstall --keep-app';
      if HasParam('/PURGE') then
        Params := Params + ' --purge';
      Exec(ExpandConstant('{app}\flock.exe'), Params, '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
    end;
  end;
  // The app's "Open Flock at sign-in" (asked in its setup, changed in Settings → General).
  if CurUninstallStep = usPostUninstall then
    RegDeleteValue(HKEY_CURRENT_USER, 'Software\Microsoft\Windows\CurrentVersion\Run', 'Flock');
end;
