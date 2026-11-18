using System.Globalization;
using System.Text.RegularExpressions;
using System.Xml.Linq;

namespace Flock.Kit.Tests;

/// <summary>
/// The app's texts in Flock.Kit's .resx files, generated from lang/ (tools/i18n), so that a broken
/// translation or a name the code uses but the files lack fails on any OS — the tray's own tests run on
/// Windows only. A language may lack texts: those fall back to English (Strings.resx).
/// </summary>
public partial class ResourcesTests
{
    public static TheoryData<string> Translations => [.. Localization.Languages.Where(language => language != "en")];

    [Theory]
    [MemberData(nameof(Translations))]
    public void TranslationsHaveKnownNamesOnly(string language)
    {
        var english = Read("");
        var translated = Read("." + language);
        Assert.Empty(translated.Keys.Where(key => !english.ContainsKey(key) && !english.ContainsKey(Stem(key) + "_other"))
            .Select(key => $"{language} has unknown {key}"));
        Assert.Empty(translated.Where(pair => pair.Value.Trim().Length == 0).Select(pair => $"{language} {pair.Key} is empty"));
    }

    [Fact]
    public void TranslationsKeepThePlaceholders()
    {
        var english = Read("");
        foreach (var language in Localization.Languages.Select(language => language == "en" ? "" : "." + language))
        {
            foreach (var (key, value) in Read(language))
            {
                var source = english.GetValueOrDefault(key) ?? english[Stem(key) + "_other"];
                var found = Placeholders(value);
                // A singular may leave the count out: "Show its worktree".
                var withoutCount = Placeholders(source).Where(name => name != "0").ToHashSet();
                Assert.True(Placeholders(source).SetEquals(found) || key.EndsWith("_one", StringComparison.Ordinal) && withoutCount.SetEquals(found),
                    $"Strings{language} {key}: \"{value}\" has other placeholders than \"{source}\"");
                if (value.Contains('{'))
                {
                    // Throws for a broken format string.
                    _ = string.Format(CultureInfo.InvariantCulture, value, Enumerable.Repeat<object>(new AnyFormat(), 10).ToArray());
                }
            }
        }
    }

    [Fact]
    public void PluralsHaveTheirForms()
    {
        var english = Read("");
        foreach (var other in english.Keys.Where(key => key.EndsWith("_other", StringComparison.Ordinal)))
        {
            Assert.True(english.ContainsKey(Stem(other) + "_one"), $"{Stem(other)}_one is missing");
        }
        foreach (var file in Sources("*.cs"))
        {
            foreach (Match match in PluralCall().Matches(File.ReadAllText(file)))
            {
                var stem = match.Groups[1].Value;
                Assert.True(english.ContainsKey(stem + "_one") && english.ContainsKey(stem + "_other"), $"{Path.GetFileName(file)}: {stem} has no plural forms");
            }
        }
    }

    [Fact]
    public void KeysDifferInMoreThanCase()
    {
        // A .resx can't hold two names that differ only in case.
        Assert.Empty(Read("").Keys.GroupBy(key => key, StringComparer.OrdinalIgnoreCase).Where(group => group.Count() > 1).Select(group => group.Key));
    }

    [Fact]
    public void XamlUsesExistingStringsOnly()
    {
        var english = Read("");
        foreach (var file in Sources("*.xaml"))
        {
            var xaml = File.ReadAllText(file);
            Assert.Empty(LocKey().Matches(xaml).Select(match => match.Groups[1].Value).Where(key => !english.ContainsKey(key))
                .Select(key => $"{Path.GetFileName(file)}: no string {key}"));
        }
    }

    [Fact]
    public void XamlHasNoUntranslatedText()
    {
        // Product names and technical values stay as they are.
        string[] kept = ["Flock", "Discord", "PHP", "Node", "PHP-CGI", "FrankenPHP", "Xdebug", "HTML", "9003", "127.0.0.1", "debug, develop",
            "npm run dev", "docker compose stop"];
        foreach (var file in Sources("*.xaml"))
        {
            var texts = XDocument.Load(file).Descendants().SelectMany(element => element.Attributes())
                .Where(attribute => TextAttributes.Contains(attribute.Name.LocalName) && attribute.Value.Length > 0 && !attribute.Value.StartsWith('{'))
                .Select(attribute => attribute.Value);
            Assert.Empty(texts.Where(text => !kept.Contains(text)).Select(text => $"{Path.GetFileName(file)}: \"{text}\""));
        }
    }

    /// <summary>An argument that takes any format specifier ({0:t}, {0:N0}).</summary>
    private sealed class AnyFormat : IFormattable
    {
        public string ToString(string? format, IFormatProvider? formatProvider) => "x";
    }

    private static readonly HashSet<string> TextAttributes =
        ["Text", "Content", "Header", "Title", "Label", "Description", "PlaceholderText", "ToolTipService.ToolTip", "AutomationProperties.Name", "BusyText"];

    /// <summary>A plural form's name without its category: Dumps_Count of Dumps_Count_few.</summary>
    private static string Stem(string key) => key[..key.LastIndexOf('_')];

    private static HashSet<string> Placeholders(string value) =>
        Placeholder().Matches(value.Replace("{{", "").Replace("}}", "")).Select(match => match.Groups[1].Value).ToHashSet();

    private static Dictionary<string, string> Read(string language) =>
        XDocument.Load(Path.Combine(Root, "Flock.Kit", $"Strings{language}.resx")).Root!.Elements("data")
            .ToDictionary(data => (string)data.Attribute("name")!, data => (string?)data.Element("value") ?? "");

    /// <summary>The code of the projects that use the strings.</summary>
    private static IEnumerable<string> Sources(string pattern) =>
        new[] { "Flock.Kit", "Flock.Tray" }.SelectMany(project => Directory.EnumerateFiles(Path.Combine(Root, project), pattern, SearchOption.AllDirectories))
            .Where(file => !file.Contains($"{Path.DirectorySeparatorChar}obj{Path.DirectorySeparatorChar}", StringComparison.Ordinal)
                           && !file.Contains($"{Path.DirectorySeparatorChar}bin{Path.DirectorySeparatorChar}", StringComparison.Ordinal));

    /// <summary>windows\, found from the test's output folder upwards.</summary>
    private static string Root { get; } = FindRoot();

    private static string FindRoot()
    {
        for (var directory = new DirectoryInfo(AppContext.BaseDirectory); directory is not null; directory = directory.Parent)
        {
            if (File.Exists(Path.Combine(directory.FullName, "Flock.Kit", "Strings.resx")))
            {
                return directory.FullName;
            }
        }
        throw new DirectoryNotFoundException("windows\\Flock.Kit\\Strings.resx not found above " + AppContext.BaseDirectory);
    }

    [GeneratedRegex(@"\{(\d+)(?:[,:][^}]*)?\}")]
    private static partial Regex Placeholder();

    [GeneratedRegex(@"Plural\(Strings\.ResourceManager,\s*""(\w+)""")]
    private static partial Regex PluralCall();

    [GeneratedRegex(@"\{l:Loc Key=(\w+)\}")]
    private static partial Regex LocKey();
}
