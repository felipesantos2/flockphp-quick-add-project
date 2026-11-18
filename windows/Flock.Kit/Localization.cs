using System.Globalization;
using System.Resources;

namespace Flock.Kit;

/// <summary>
/// The app's languages and the formatting its translated strings need, English being the source and
/// fallback. Strings come from Strings.resx and its satellites, looked up in
/// CultureInfo.CurrentUICulture, which the app sets from the Language preference at startup. The
/// languages and their plural rules are in Languages.g.cs, which tools/i18n generates from lang/.
/// </summary>
public static partial class Localization
{
    /// <summary>The Language preference that follows the system's preferred languages.</summary>
    public const string System = "system";

    /// <summary>
    /// The language to show: the preferred one when it is translated, else the first of the system's
    /// preferred languages (most preferred first, e.g. "de-DE", "cs-CZ") that is, else English.
    /// </summary>
    public static string Resolve(string? preference, IEnumerable<string> systemLanguages)
    {
        if (preference is not null && Languages.Contains(preference))
        {
            return preference;
        }
        foreach (var language in systemLanguages)
        {
            // A regional variant the app has ("pt-BR"), else the language ("de-AT" is "de").
            var primary = language.Split('-', '_')[0].ToLowerInvariant();
            var found = Languages.FirstOrDefault(code => string.Equals(code, language.Replace('_', '-'), StringComparison.OrdinalIgnoreCase))
                        ?? Languages.FirstOrDefault(code => code == primary);
            if (found is not null)
            {
                return found;
            }
        }
        return "en";
    }

    /// <summary>A translated format string filled in with the user's number and date formats.</summary>
    public static string Format(string format, params object?[] args) => string.Format(CultureInfo.CurrentCulture, format, args);

    /// <summary>
    /// A count in a sentence ("3 sites", "před 3 minutami"): the resource <c>key_one</c>, <c>key_few</c>,
    /// ... or <c>key_other</c> for the count in the current UI language, with the count as {0} (with digit
    /// grouping: 1,234) and args from {1} on. A missing form falls back to <c>_other</c>.
    /// </summary>
    public static string Plural(ResourceManager resources, string key, long count, params object?[] args)
    {
        var culture = CultureInfo.CurrentUICulture;
        var format = resources.GetString($"{key}_{PluralCategory(count, culture)}", culture)
                     ?? resources.GetString(key + "_other", culture)
                     ?? throw new MissingManifestResourceException($"No plural resource {key}_other.");
        return string.Format(CultureInfo.CurrentCulture, format, [count.ToString("N0", CultureInfo.CurrentCulture), .. args]);
    }

    /// <summary>
    /// The CLDR plural category of a whole number in a culture's language: "one" or "other" in English;
    /// in Czech also "few" for 2–4 (1 web, 2 weby, 5 webů).
    /// </summary>
    public static string PluralCategory(long count, CultureInfo culture)
    {
        var language = Languages.Contains(culture.Name) ? culture.Name : culture.TwoLetterISOLanguageName;
        return PluralCategory(language, count);
    }
}
