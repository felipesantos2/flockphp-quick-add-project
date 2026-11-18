using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
namespace Flock.Tray.Settings;

/// <summary>A warning in a site's details (or, as a hint, a note with an info icon), which wraps.</summary>
internal sealed partial class DetailWarning : UserControl
{
    public static readonly DependencyProperty TextProperty = DependencyProperty.Register(
        nameof(Text), typeof(string), typeof(DetailWarning), new PropertyMetadata("", (control, _) => ((DetailWarning)control).Apply()));

    /// <summary>A hint: an info icon, and text that isn't selectable.</summary>
    public static readonly DependencyProperty IsHintProperty = DependencyProperty.Register(
        nameof(IsHint), typeof(bool), typeof(DetailWarning), new PropertyMetadata(false, (control, _) => ((DetailWarning)control).Apply()));

    public DetailWarning()
    {
        InitializeComponent();
    }

    public string Text
    {
        get => (string)GetValue(TextProperty);
        set => SetValue(TextProperty, value);
    }

    public bool IsHint
    {
        get => (bool)GetValue(IsHintProperty);
        set => SetValue(IsHintProperty, value);
    }

    private void Apply()
    {
        Label.Text = Text;
        Label.IsTextSelectionEnabled = !IsHint;
        Icon.Glyph = IsHint ? Ui.GlyphInfo : Ui.GlyphWarning;
    }
}
