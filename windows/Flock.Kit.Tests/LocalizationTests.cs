using System.Globalization;
using System.Resources;

namespace Flock.Kit.Tests;

public class LocalizationTests
{
    [Theory]
    [InlineData(0, "other")]
    [InlineData(1, "one")]
    [InlineData(2, "few")]
    [InlineData(3, "few")]
    [InlineData(4, "few")]
    [InlineData(5, "other")]
    [InlineData(11, "other")]
    [InlineData(21, "other")]
    [InlineData(22, "other")]
    [InlineData(101, "other")]
    public void CzechHasOneFewAndOther(long count, string category)
    {
        Assert.Equal(category, Localization.PluralCategory(count, CultureInfo.GetCultureInfo("cs-CZ")));
    }

    [Theory]
    [InlineData("en"), InlineData("es"), InlineData("es-MX")]
    public void EnglishAndSpanishHaveOneAndOther(string language)
    {
        var culture = CultureInfo.GetCultureInfo(language);
        Assert.Equal(["other", "one", "other", "other", "other"], new long[] { 0, 1, 2, 4, 5 }.Select(n => Localization.PluralCategory(n, culture)));
    }

    [Fact]
    public void PluralPicksTheFormForTheCountAndFallsBackToOther()
    {
        var resources = new FakeResources(new()
        {
            ["Sites_one"] = "{0} site of {1}",
            ["Sites_other"] = "{0} sites of {1}",
            ["cs:Sites_one"] = "{0} web z {1}",
            ["cs:Sites_few"] = "{0} weby z {1}",
            ["cs:Sites_other"] = "{0} webů z {1}",
            ["Mail_one"] = "{0} mail",
            ["Mail_other"] = "{0} mails",
        });

        Assert.Equal(["1 site of x", "3 sites of x"], new[] { 1, 3 }.Select(n => Localization.Plural(resources, "Sites", n, "x")));
        WithUiCulture("cs-CZ", () =>
        {
            Assert.Equal(["1 web z x", "3 weby z x", "5 webů z x"], new[] { 1, 3, 5 }.Select(n => Localization.Plural(resources, "Sites", n, "x")));
            // Without a Czech few form (here: no translation at all), the other form is used.
            Assert.Equal("3 mails", Localization.Plural(resources, "Mail", 3));
        });
        Assert.Throws<MissingManifestResourceException>(() => Localization.Plural(resources, "Missing", 2));
    }

    [Fact]
    public void FormatUsesTheRegionalNumberFormat()
    {
        WithCulture("cs-CZ", () => Assert.Equal("1,5 MB", Localization.Format("{0} MB", 1.5)));
        Assert.Equal("1.5 MB", Localization.Format("{0} MB", 1.5));
    }

    [Theory]
    [InlineData("cs", new[] { "en-US" }, "cs")]
    [InlineData("en", new[] { "cs-CZ" }, "en")]
    [InlineData("system", new[] { "de-DE", "cs-CZ", "en-US" }, "cs")]
    [InlineData("system", new[] { "es-419" }, "es")]
    [InlineData("system", new[] { "en-GB", "es-ES" }, "en")]
    [InlineData("system", new[] { "de-DE", "fr-FR" }, "en")]
    [InlineData("system", new string[0], "en")]
    [InlineData("fr", new[] { "es-ES" }, "es")]
    [InlineData(null, new[] { "CS" }, "cs")]
    public void ResolvesThePreferenceOrTheFirstTranslatedSystemLanguage(string? preference, string[] system, string expected)
    {
        Assert.Equal(expected, Localization.Resolve(preference, system));
    }

    private static void WithUiCulture(string name, Action action)
    {
        var previous = CultureInfo.CurrentUICulture;
        CultureInfo.CurrentUICulture = CultureInfo.GetCultureInfo(name);
        try
        {
            action();
        }
        finally
        {
            CultureInfo.CurrentUICulture = previous;
        }
    }

    private static void WithCulture(string name, Action action)
    {
        var previous = CultureInfo.CurrentCulture;
        CultureInfo.CurrentCulture = CultureInfo.GetCultureInfo(name);
        try
        {
            action();
        }
        finally
        {
            CultureInfo.CurrentCulture = previous;
        }
    }

    /// <summary>Resources keyed "name" (neutral) and "cs:name", with ResourceManager's fallback to the neutral ones.</summary>
    private sealed class FakeResources(Dictionary<string, string> values) : ResourceManager
    {
        public override string? GetString(string name, CultureInfo? culture) =>
            values.GetValueOrDefault($"{culture?.TwoLetterISOLanguageName}:{name}") ?? values.GetValueOrDefault(name);
    }
}
