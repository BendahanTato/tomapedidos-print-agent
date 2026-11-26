; Inno Setup script for TomaPedidos Print Agent (Windows)
; Generates a single double-clickable Setup .exe that installs the binary
; and launches the step-by-step configuration wizard.

#ifndef AppVersion
  #define AppVersion "0.4.0"
#endif
#ifndef AppArch
  #define AppArch "amd64"
#endif
#ifndef AppExe
  #define AppExe "..\print-agent-windows-amd64.exe"
#endif

[Setup]
AppId={{8D6B5F58-4C37-4F3B-9C9C-8F9E7A2B1D01}
AppName=TomaPedidos Print Agent
AppVersion={#AppVersion}
AppPublisher=TomaPedidos
DefaultDirName={autopf}\Tomapedidos\PrintAgent
DefaultGroupName=TomaPedidos Print Agent
DisableProgramGroupPage=yes
PrivilegesRequired=admin
ArchitecturesAllowed={#if AppArch == "arm64"}arm64{#else}x64compatible{#endif}
ArchitecturesInstallIn64BitMode={#if AppArch == "arm64"}arm64{#else}x64compatible{#endif}
OutputDir=..\dist
OutputBaseFilename=print-agent-windows-{#AppArch}-setup
Compression=lzma2
SolidCompression=yes
WizardStyle=modern

[Languages]
Name: "spanish"; MessagesFile: "compiler:Languages\Spanish.isl"

[Files]
Source: "{#AppExe}"; DestDir: "{app}"; DestName: "print-agent.staging.exe"; Flags: ignoreversion
Source: "install.ps1"; DestDir: "{app}"; Flags: ignoreversion
Source: "instalar.bat"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\Configurar Agente de Impresion"; Filename: "{app}\instalar.bat"
Name: "{group}\Desinstalar TomaPedidos Print Agent"; Filename: "{uninstallexe}"

[Run]
Filename: "powershell.exe"; Parameters: "-NoProfile -WindowStyle Minimized -ExecutionPolicy Bypass -File ""{app}\install.ps1"" -LocalBinary ""{app}\print-agent.staging.exe"" -GuiSetup"; StatusMsg: "Configurando servicio y asistente de impresoras..."; Flags: waituntilterminated

[UninstallRun]
Filename: "{app}\print-agent.exe"; Parameters: "stop-svc"; Flags: runhidden; RunOnceId: "StopAgentSvc"
Filename: "{app}\print-agent.exe"; Parameters: "uninstall"; Flags: runhidden; RunOnceId: "UninstallAgentSvc"
