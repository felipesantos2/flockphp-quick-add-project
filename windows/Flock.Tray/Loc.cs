using Microsoft.UI.Xaml.Markup;

namespace Flock.Tray;

/// <summary>
/// A translated string in XAML, from Strings.resx in the current UI language:
/// <c>Text="{l:Loc Key=General_Daemon}"</c> with <c>xmlns:l="using:Flock.Tray"</c>.
/// </summary>
[MarkupExtensionReturnType(ReturnType = typeof(string))]
public sealed partial class Loc : MarkupExtension
{
    public string Key { get; set; } = "";

    // A missing key shows itself rather than nothing; ResourcesTests fails for it first.
    protected override object ProvideValue() => Strings.ResourceManager.GetString(Key, Strings.Culture) ?? Key;
}
