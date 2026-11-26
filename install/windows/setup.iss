; Inno Setup installer script for TomaPedidos Print Agent (Windows).
; Produces a single double-click Setup.exe that requests UAC elevation,
; installs print-agent.exe under Program Files, configures ProgramData ACLs
; with locale-independent SIDs, starts the Windows Service, verifies /health,
; and offers to open http://127.0.0.1:4510.

#ifndef MyAppVersion
  #define MyAppVersion "0.0.0"
#endif
#ifndef MyAppArch
  #define MyAppArch "amd64"
#endif
#ifndef MyBinarySource
  #define MyBinarySource "..\..\print-agent-windows-amd64.exe"
#endif
#ifndef MyOutputDir
  #define MyOutputDir "..\.."
#endif
#ifndef MyOutputBaseFilename
  #define MyOutputBaseFilename "TomaPedidos-PrintAgent-Setup-windows-amd64"
#endif

[Setup]
AppId={{8F6D72B4-5E2B-4E7A-9C91-7D3A21E45100}
AppName=TomaPedidos Print Agent
AppVersion={#MyAppVersion}
AppPublisher=TomaPedidos
AppPublisherURL=https://github.com/BendahanTato/tomapedidos-print-agent
DefaultDirName={autopf}\Tomapedidos\PrintAgent
DefaultGroupName=TomaPedidos
DisableProgramGroupPage=yes
DisableDirPage=yes
PrivilegesRequired=admin
CloseApplications=no
OutputDir={#MyOutputDir}
OutputBaseFilename={#MyOutputBaseFilename}
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
UninstallDisplayName=TomaPedidos Print Agent
#if MyAppArch == "arm64"
ArchitecturesAllowed=arm64
ArchitecturesInstallIn64BitMode=arm64
#else
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
#endif

[Languages]
Name: "spanish"; MessagesFile: "compiler:Languages\Spanish.isl"
Name: "english"; MessagesFile: "compiler:Default.isl"

[Dirs]
Name: "{commonappdata}\Tomapedidos\config"
Name: "{commonappdata}\Tomapedidos\data"
Name: "{commonappdata}\Tomapedidos\logs"

[Files]
Source: "{#MyBinarySource}"; DestDir: "{app}"; DestName: "print-agent.exe"; Flags: ignoreversion
Source: "..\..\templates\*.tmpl"; DestDir: "{app}\templates"; Flags: ignoreversion

[Icons]
Name: "{group}\Panel TomaPedidos Print Agent"; Filename: "http://127.0.0.1:4510"
Name: "{group}\Desinstalar TomaPedidos Print Agent"; Filename: "{uninstallexe}"

[Run]
Filename: "{app}\print-agent.exe"; Parameters: "install --config ""{commonappdata}\Tomapedidos\config\printers.json"""; StatusMsg: "Registrando servicio de Windows..."; Flags: runhidden waituntilterminated
Filename: "{app}\print-agent.exe"; Parameters: "start-svc"; StatusMsg: "Iniciando TomaPedidos Print Agent..."; Flags: runhidden waituntilterminated
Filename: "http://127.0.0.1:4510"; Description: "Abrir el panel para configurar y probar la impresora (http://127.0.0.1:4510)"; Flags: shellexec postinstall skipifsilent

[UninstallRun]
Filename: "{app}\print-agent.exe"; Parameters: "uninstall"; Flags: runhidden waituntilterminated; RunOnceId: "UninstallPrintAgentSvc"

[Code]
procedure StopExistingService();
var
  ResultCode: Integer;
  I: Integer;
begin
  Exec(ExpandConstant('{sys}\sc.exe'), 'stop com.tomapedidos.print-agent', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  for I := 1 to 20 do
  begin
    if not FileOrDirExists(ExpandConstant('{app}\print-agent.exe')) then
      Break;
    if RenameFile(ExpandConstant('{app}\print-agent.exe'), ExpandConstant('{app}\print-agent.exe')) then
      Break;
    Sleep(500);
  end;
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
begin
  StopExistingService();
  Result := '';
end;

function WaitForAgentHealth(): Boolean;
var
  HttpReq: Variant;
  Attempt: Integer;
begin
  Result := False;
  for Attempt := 1 to 20 do
  begin
    try
      HttpReq := CreateOleObject('WinHttp.WinHttpRequest.5.1');
      HttpReq.Open('GET', 'http://127.0.0.1:4510/health', False);
      HttpReq.SetTimeouts(1000, 1000, 1000, 1000);
      HttpReq.Send('');
      if HttpReq.Status = 200 then
      begin
        Result := True;
        Exit;
      end;
    except
    end;
    Sleep(1000);
  end;
end;

function ReadLogTail(): String;
var
  LogFile: String;
  Lines: TArrayOfString;
  StartIdx, I: Integer;
begin
  Result := '';
  LogFile := ExpandConstant('{commonappdata}\Tomapedidos\logs\print-agent.log');
  if LoadStringsFromFile(LogFile, Lines) then
  begin
    StartIdx := GetArrayLength(Lines) - 8;
    if StartIdx < 0 then
      StartIdx := 0;
    for I := StartIdx to GetArrayLength(Lines) - 1 do
      Result := Result + Lines[I] + #13#10;
  end;
end;

procedure CurStepChanged(CurStep: TSetupStep);
var
  Tail: String;
  Msg: String;
begin
  if CurStep = ssPostInstall then
  begin
    WizardForm.StatusLabel.Caption := 'Verificando que el servicio responda en http://127.0.0.1:4510/health...';
    if not WaitForAgentHealth() then
    begin
      Tail := ReadLogTail();
      Msg := 'El servicio se instaló pero no respondió en http://127.0.0.1:4510/health.' + #13#10 +
             'Revisá si algún antivirus o puerto bloqueó el proceso.' + #13#10 +
             'Podés ejecutar: "' + ExpandConstant('{app}\print-agent.exe') + '" doctor';
      if Tail <> '' then
        Msg := Msg + #13#10 + #13#10 + 'Últimas líneas del log:' + #13#10 + Tail;
      Log(Msg);
      if not WizardSilent() then
        MsgBox(Msg, mbError, MB_OK);
    end;
  end;
end;
