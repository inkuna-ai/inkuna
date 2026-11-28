Unicode true

# The Windows installer a release ships, built by `task package -- -nsis`
# after task build:go. wails build -nsis writes
# wails_tools.nsh beside this file from wails.json each time, so it is not
# committed.
#
# It installs for the current user, where inkunactl install puts the app
# (internal/install/layout.go): %LOCALAPPDATA%\Programs\Inkuna, with a Start
# menu shortcut. So it needs no administrator rights, and an install from
# either one replaces the other. package_test.go keeps the two in step.
!define REQUEST_EXECUTION_LEVEL "user"
!define WAILS_INSTALL_SCOPE "user"

!include "wails_tools.nsh"

# The CLI the app ships beside, which task build:go puts in the repository's
# bin/. Paths are relative to this file.
!ifndef ARG_INKUNACTL_BINARY
    !define ARG_INKUNACTL_BINARY "..\..\..\bin\inkunactl.exe"
!endif

# The version information for these two must have four parts.
VIProductVersion "${INFO_PRODUCTVERSION}.0"
VIFileVersion    "${INFO_PRODUCTVERSION}.0"

VIAddVersionKey "CompanyName"     "${INFO_COMPANYNAME}"
VIAddVersionKey "FileDescription" "${INFO_PRODUCTNAME} Installer"
VIAddVersionKey "ProductVersion"  "${INFO_PRODUCTVERSION}"
VIAddVersionKey "FileVersion"     "${INFO_PRODUCTVERSION}"
VIAddVersionKey "LegalCopyright"  "${INFO_COPYRIGHT}"
VIAddVersionKey "ProductName"     "${INFO_PRODUCTNAME}"

ManifestDPIAware true

!include "MUI.nsh"

!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
!define MUI_ABORTWARNING
!define MUI_FINISHPAGE_RUN "$INSTDIR\${PRODUCT_EXECUTABLE}"

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_LICENSE "..\..\..\LICENSE.md"
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "English"

Name "${INFO_PRODUCTNAME}"
OutFile "..\..\bin\${INFO_PROJECTNAME}-${ARCH}-installer.exe"
# There is no directory page: inkunactl install and update look for the app
# here.
InstallDir "$LOCALAPPDATA\Programs\${INFO_PRODUCTNAME}"
ShowInstDetails show

Function .onInit
    !insertmacro wails.checkArchitecture
FunctionEnd

Section
    !insertmacro wails.setShellContext

    !insertmacro wails.webview2runtime

    SetOutPath $INSTDIR

    !insertmacro wails.files
    File "/oname=inkunactl.exe" "${ARG_INKUNACTL_BINARY}"
    File "/oname=LICENSE.md" "..\..\..\LICENSE.md"
    File "/oname=NOTICE.txt" "..\..\..\NOTICE"
    File "/oname=THIRD_PARTY_NOTICES.txt" "..\..\..\THIRD_PARTY_NOTICES"

    CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"

    !insertmacro wails.writeUninstaller
SectionEnd

Section "uninstall"
    !insertmacro wails.setShellContext

    # The configuration, the database, and the WebView2 profile are the
    # user's, so uninstalling removes only the program and its shortcut.
    RMDir /r $INSTDIR

    Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"

    !insertmacro wails.deleteUninstaller
SectionEnd
