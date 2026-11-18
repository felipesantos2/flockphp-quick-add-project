using System.Globalization;
using Flock.Kit;

namespace Flock.Tray;

/// <summary>
/// The UI language, chosen once at startup from the Language preference (Settings → General): a
/// language changes only with a restart, since text is set when each window is built.
/// </summary>
internal static class AppLanguage
{
    /// <summary>The language the running app shows: "en" or another of Localization.Languages.</summary>
    public static string Current { get; private set; } = "en";

    /// <summary>The system's preferred UI languages at startup, before Apply set the UI culture.</summary>
    private static string[] systemLanguages = [];

    /// <summary>
    /// Sets the UI culture of this thread and of every thread started later, before any window exists.
    /// WinUI's own text (a text box's context menu) keeps following the Windows display language:
    /// Microsoft.Windows.Globalization.ApplicationLanguages.PrimaryLanguageOverride would change it, but for
    /// unpackaged apps it is only documented for languages the app declares, and reported to throw.
    /// </summary>
    public static void Apply(string preference)
    {
        // Windows' preferred UI languages in order (Win32: WinRT's GlobalizationPreferences.Languages, the
        // full Settings → Language list, isn't safe before App.Run sets up the COM wrappers), then the
        // display language .NET started with.
        systemLanguages = [.. Native.PreferredUiLanguages(), CultureInfo.CurrentUICulture.Name];
        Current = Localization.Resolve(preference, systemLanguages);
        var culture = CultureInfo.GetCultureInfo(Current);
        CultureInfo.DefaultThreadCurrentUICulture = culture;
        CultureInfo.CurrentUICulture = culture;
    }

    /// <summary>The language a preference would show after a restart.</summary>
    public static string Resolve(string preference) => Localization.Resolve(preference, systemLanguages);
}
