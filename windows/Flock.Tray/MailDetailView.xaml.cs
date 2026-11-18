using System.ComponentModel;
using System.Globalization;
using Flock.Kit;
using Flock.Tray.Infrastructure;
using Flock.Tray.ViewModels;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
namespace Flock.Tray;

/// <summary>One message's headers, attachments, and content modes.</summary>
internal sealed partial class MailDetailView : Grid, IAsyncDisposable
{
    private readonly MailDetail message;
    private readonly IntPtr owner;
    private readonly IShellLauncher shell;
    private readonly MailDetailViewModel viewModel;
    private HtmlView? html;
    private Task htmlLoad = Task.CompletedTask;
    private bool released;

    public MailDetailView(IntPtr owner, MailDetailViewModel viewModel, MailDetail message, IShellLauncher shell)
    {
        this.owner = owner;
        this.viewModel = viewModel;
        this.message = message;
        this.shell = shell;
        InitializeComponent();
        Subject.Text = message.Subject.Length == 0 ? Strings.Mail_NoSubject : message.Subject;
        From.Text = message.From;
        To.Text = string.Join(", ", message.To);
        Date.Text = message.Date.ToLocalTime().ToString("f", CultureInfo.CurrentCulture);
        Inbox.Text = message.Inbox;
        if (message.Cc.Count > 0)
        {
            Cc.Text = string.Join(", ", message.Cc);
            Cc.Visibility = Visibility.Visible;
            CcLabel.Visibility = Visibility.Visible;
        }
        foreach (var attachment in message.Attachments)
        {
            Attachments.Children.Add(Ui.AsyncButton($"{attachment.Filename} ({Ui.Bytes(attachment.Size)})", () => viewModel.SaveAttachmentAsync(attachment, owner),
                glyph: Ui.GlyphAttach, tooltip: Strings.Mail_SaveAttachment));
        }
        AttachmentsScroll.Visibility = message.Attachments.Count > 0 ? Visibility.Visible : Visibility.Collapsed;
        this.viewModel.PropertyChanged += ViewModelChanged;
        Modes.SelectedItem = Modes.Items[message.Html.Length == 0 ? 1 : 0];
        RenderMode();
    }

    public async ValueTask DisposeAsync()
    {
        if (released) return;
        released = true;
        viewModel.PropertyChanged -= ViewModelChanged;
        if (html is not null)
        {
            await htmlLoad;
            await html.ReleaseAsync();
            html = null;
        }
    }

    public static ScrollViewer Monospaced(string text)
    {
        return new ScrollViewer
        {
            Content = new TextBlock { Text = text, FontFamily = Ui.Mono, FontSize = 13, IsTextSelectionEnabled = true, Margin = new Thickness(16, 12, 16, 12) },
            HorizontalScrollBarVisibility = ScrollBarVisibility.Auto,
            HorizontalScrollMode = ScrollMode.Auto
        };
    }

    private async void ModeChanged(SelectorBar sender, SelectorBarSelectionChangedEventArgs args)
    {
        if (Modes.SelectedItem is SelectorBarItem item && item.Tag is string mode)
        {
            await viewModel.SetModeAsync(mode);
            RenderMode();
        }
    }

    private void ViewModelChanged(object? sender, PropertyChangedEventArgs args)
    {
        if (args.PropertyName is nameof(MailDetailViewModel.Raw) or nameof(MailDetailViewModel.RawError) or nameof(MailDetailViewModel.IsRawLoading)
            or nameof(MailDetailViewModel.Error)) RenderMode();
    }

    private void RenderMode()
    {
        if (released) return;
        switch (viewModel.Mode)
        {
            case "HTML":
                if (message.Html.Length == 0)
                {
                    ContentHost.Content = Ui.EmptyState(Ui.GlyphDocument, Strings.Mail_NoHtmlPart);
                    break;
                }
                html ??= new HtmlView(false, true, true, shell);
                htmlLoad = html.LoadAsync(message.Html);
                ContentHost.Content = html;
                break;
            case "Text": ContentHost.Content = Monospaced(message.Text.Length == 0 ? Strings.Mail_NoTextPart : message.Text); break;
            case "Raw":
                if (viewModel.IsRawLoading) ContentHost.Content = new ProgressRing { Width = 32, Height = 32 };
                else if (viewModel.RawError is { } error)
                    ContentHost.Content = Ui.EmptyState(Ui.GlyphWarning, Strings.Mail_CouldNotLoadRaw, error, Ui.AsyncButton(Strings.Common_Retry, () => SetModeAsync("Raw"), true));
                else ContentHost.Content = Monospaced(viewModel.Raw ?? Strings.Common_EmptyParenthesized);
                break;
            default: ContentHost.Content = Headers(); break;
        }
    }

    private Task SetModeAsync(string mode)
    {
        return SetModeAndRenderAsync(mode);
    }

    private async Task SetModeAndRenderAsync(string mode)
    {
        await viewModel.SetModeAsync(mode);
        RenderMode();
    }

    private ScrollViewer Headers()
    {
        var grid = new Grid { ColumnSpacing = 16, RowSpacing = 6, Margin = new Thickness(16, 12, 16, 12) };
        grid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(200) });
        grid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        for (var i = 0; i < message.Headers.Count; i++)
        {
            grid.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
            var name = Ui.Text(message.Headers[i].Name, foreground: Ui.SecondaryText, selectable: true);
            var value = Ui.Text(message.Headers[i].Value, selectable: true);
            SetRow(name, i);
            SetRow(value, i);
            SetColumn(value, 1);
            grid.Children.Add(name);
            grid.Children.Add(value);
        }
        return new ScrollViewer { Content = grid };
    }
}