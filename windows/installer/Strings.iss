; Generated from lang/ by tools/i18n: edit lang/*/app.json, then run make i18n.
; Flock.iss includes it; %1 is a message's first argument.

[Languages]
Name: "en"; MessagesFile: "compiler:Default.isl"
Name: "cs"; MessagesFile: "compiler:Languages\Czech.isl"
Name: "es"; MessagesFile: "compiler:Languages\Spanish.isl"

[CustomMessages]
en.Installer_OpenFlock=Open Flock
cs.Installer_OpenFlock=Otevřít Flock
es.Installer_OpenFlock=Abrir Flock
en.Installer_PrivilegedDeclined=Flock needs administrator permission to update its DNS rule, hosts helper or certificate authority. The update was not installed; Flock stays at its current version.
cs.Installer_PrivilegedDeclined=Flock potřebuje oprávnění správce, aby mohl aktualizovat své DNS pravidlo, pomocníka pro soubor hosts nebo certifikační autoritu. Aktualizace nebyla nainstalována; Flock zůstává ve své současné verzi.
es.Installer_PrivilegedDeclined=Flock necesita permiso de administrador para actualizar su regla DNS, el asistente de hosts o la autoridad de certificación. La actualización no se instaló; Flock se queda en su versión actual.
en.Installer_PrivilegedFailed=the steps that need administrator permission failed
cs.Installer_PrivilegedFailed=kroky, které vyžadují oprávnění správce, selhaly
es.Installer_PrivilegedFailed=fallaron los pasos que necesitan permiso de administrador
; %1: why (the CLI's English error, or PrivilegedFailed)
en.Installer_UpdateNotInstalled=The update was not installed: %1
cs.Installer_UpdateNotInstalled=Aktualizace nebyla nainstalována: %1
es.Installer_UpdateNotInstalled=La actualización no se instaló: %1
