using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
namespace Flock.Tray.Settings;

/// <summary>
///     A labelled setting in a site's details: the label in a fixed column, so the controls line up,
///     and the control (its content) on the left of the rest. Its template is in DetailStyles.xaml.
/// </summary>
internal sealed partial class DetailField : ContentControl
{
    public static readonly DependencyProperty LabelProperty = DependencyProperty.Register(
        nameof(Label), typeof(string), typeof(DetailField), new PropertyMetadata(""));

    /// <summary>Center for a one-line control; Top for a column of them, so the label lines up with the first.</summary>
    public static readonly DependencyProperty LabelAlignmentProperty = DependencyProperty.Register(
        nameof(LabelAlignment), typeof(VerticalAlignment), typeof(DetailField), new PropertyMetadata(VerticalAlignment.Center));

    public string Label
    {
        get => (string)GetValue(LabelProperty);
        set => SetValue(LabelProperty, value);
    }

    public VerticalAlignment LabelAlignment
    {
        get => (VerticalAlignment)GetValue(LabelAlignmentProperty);
        set => SetValue(LabelAlignmentProperty, value);
    }
}
