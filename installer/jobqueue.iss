#ifndef AppVersion
  #define AppVersion "dev"
#endif
#define AppName "JobQueue"
#define AppExe "jobqueue-server.exe"

[Setup]
AppId={{42a484c1-67ce-4fbd-b3aa-28e7504ec814}
AppName={#AppName}
AppVersion={#AppVersion}
AppPublisher=@the_cleaner2
DefaultDirName={autopf}\{#AppName}
DefaultGroupName={#AppName}
DisableProgramGroupPage=yes
LicenseFile=..\LICENSE
OutputDir=output
OutputBaseFilename=jobqueue-setup_{#AppVersion}
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
PrivilegesRequired=lowest
PrivilegesRequiredOverridesAllowed=dialog
UninstallDisplayIcon={app}\{#AppExe}

[Tasks]
Name: "desktopicon"; Description: "Create a &desktop shortcut"; Flags: unchecked

[Files]
Source: "..\build\jobqueue-server.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\README.md"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\LICENSE"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\JobQueue Server"; Filename: "{app}\{#AppExe}"
Name: "{group}\Uninstall JobQueue"; Filename: "{uninstallexe}"
Name: "{autodesktop}\JobQueue Server"; Filename: "{app}\{#AppExe}"; Tasks: desktopicon

[Run]
Filename: "{app}\{#AppExe}"; Description: "Start JobQueue server now"; Flags: nowait postinstall skipifsilent unchecked