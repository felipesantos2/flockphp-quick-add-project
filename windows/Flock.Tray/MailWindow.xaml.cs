using System.ComponentModel;
using Flock.Kit;
using Flock.Tray.ViewModels;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
namespace Flock.Tray;

/// <summary>Caught mail: the list on the left, the selected message on the right.</summary>
internal sealed partial class MailWindow : FlockWindow
{
    private readonly Task initialRefresh;
    private readonly MailViewModel viewModel;
    private MailDetailView? detailView;
    private bool rendering;

    public MailWindow(MailViewModel viewModel) : base(Strings.Settings_Pane_Mail, 1000, 660)
    {
        this.viewModel = viewModel;
        InitializeComponent();
        InitializeChrome(WindowRoot, WindowTitleBar, WindowCaption, WindowIcon);
        WindowBody.DataContext = viewModel;
        DetailHost.Content = NoSelection();
        viewModel.PropertyChanged += ViewModelChanged;
        Closed += (_, _) => viewModel.PropertyChanged -= ViewModelChanged;
        Render();
        initialRefresh = viewModel.RefreshAsync();
        Ui.SettingsShortcut(WindowRoot);
    }

    internal void SelectFirst()
    {
        MailList.SelectedIndex = 0;
    }

    private void ViewModelChanged(object? sender, PropertyChangedEventArgs args)
    {
        Render();
    }

    private void Render()
    {
        Ui.ShowError(ErrorBar, viewModel.Error);
        ClearButton.IsEnabled = viewModel.CanClear;
        rendering = true;
        MailList.SelectedItem = viewModel.SelectedId is { } id ? viewModel.Messages.FirstOrDefault(message => message.Id == id) : null;
        EmptyStateHost.Content = viewModel.Messages.Count == 0
            ? Ui.EmptyState(Ui.GlyphMail, Strings.Mail_NoMail, Localization.Format(Strings.Mail_NoMailDescription, viewModel.SmtpAddress))
            : null;
        rendering = false;
        if (viewModel.SelectedId is null)
        {
            DetailHost.Content = NoSelection();
        }
    }

    private StackPanel NoSelection()
    {
        if (viewModel.Env.Length == 0)
        {
            return Ui.EmptyState(Ui.GlyphMail, Strings.Mail_NoMessageSelected);
        }
        var note = Ui.Caption(Strings.Mail_UsernameInbox);
        note.TextAlignment = TextAlignment.Center;
        return Ui.EmptyState(Ui.GlyphMail, Strings.Mail_CatchTitle, Strings.Mail_CatchDescription, Ui.Column(8, Ui.CodeBlock(viewModel.Env), note));
    }

    private async void SelectionChanged(object sender, SelectionChangedEventArgs args)
    {
        if (!rendering)
        {
            await ShowDetailAsync((MailList.SelectedItem as MailViewModel.MailRow)?.Id);
        }
    }

    private async void ClearClicked(object sender, RoutedEventArgs args)
    {
        var dialog = new ContentDialog
        {
            Title = Strings.Mail_DeleteAllQuestion,
            PrimaryButtonText = Strings.Mail_DeleteAll,
            CloseButtonText = Strings.Common_Cancel,
            DefaultButton = ContentDialogButton.Close,
            XamlRoot = WindowRoot.XamlRoot
        };
        if (await dialog.ShowAsync() == ContentDialogResult.Primary)
        {
            await viewModel.SelectAsync(null);
            DetailHost.Content = NoSelection();
            await viewModel.ClearCommand.ExecuteAsync(null);
        }
    }

    private async Task ShowDetailAsync(string? id)
    {
        var selection = viewModel.SelectAsync(id);
        DetailHost.Content = new ProgressRing { Width = 32, Height = 32 };
        if (detailView is not null)
        {
            await detailView.DisposeAsync();
            detailView = null;
        }
        await selection;
        if (id is null || viewModel.SelectedId != id)
        {
            if (viewModel.SelectedId == id)
            {
                DetailHost.Content = NoSelection();
            }
            return;
        }
        if (viewModel.Detail.Current is null)
        {
            DetailHost.Content = Ui.EmptyState(Ui.GlyphWarning, Strings.Mail_CouldNotLoad, viewModel.Detail.Error);
            return;
        }
        detailView = new MailDetailView(Hwnd, viewModel.Detail, viewModel.Detail.Current, viewModel.Model.Shell);
        DetailHost.Content = detailView;
    }

    public override async Task ReleaseAsync()
    {
        if (detailView is not null)
        {
            await detailView.DisposeAsync();
            detailView = null;
        }
        await initialRefresh;
        await viewModel.DisposeAsync();
    }
}