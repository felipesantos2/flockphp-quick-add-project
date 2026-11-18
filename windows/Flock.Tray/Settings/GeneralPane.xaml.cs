using System.ComponentModel;
using System.Security;
using Flock.Kit;
using Flock.Tray.Infrastructure;
using Flock.Tray.ViewModels;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
namespace Flock.Tray.Settings;

internal sealed partial class GeneralPane : ISettingsPane
{
    private readonly bool loaded;
    private readonly RuntimeService model;
    private readonly GeneralViewModel viewModel;
    private readonly SettingsWindow window;

    public GeneralPane(SettingsWindow window, RuntimeService model)
    {
        this.window = window;
        this.model = model;
        InitializeComponent();
        viewModel = new GeneralViewModel(model);
        DataContext = viewModel;
        Startup.IsOn = Native.StartsAtSignIn;
        LanguageHost.Children.Add(LanguageSetting.Create(model.Preferences));
        viewModel.PropertyChanged += ViewModelChanged;
        loaded = true;
        UpdateParked();
    }

    public string Title => Strings.General_Title;
    public string Subtitle => Strings.General_Subtitle;
    public UIElement Root => this;
    public bool ScrollsItself => false;
    public Task ActivateAsync(CancellationToken hidden)
    {
        Startup.IsOn = Native.StartsAtSignIn;
        return viewModel.RefreshAsync(hidden);
    }
    public void Hidden() { }

    public Task ReleaseAsync()
    {
        viewModel.PropertyChanged -= ViewModelChanged;
        viewModel.Dispose();
        return Task.CompletedTask;
    }

    private void ViewModelChanged(object? sender, PropertyChangedEventArgs args)
    {
        if (args.PropertyName is nameof(GeneralViewModel.Config))
        {
            UpdateParked();
        }
    }

    private void UpdateParked()
    {
        var paths = viewModel.Config?.Parked ?? [];
        if (viewModel.SelectedParked is null)
            return;

        Parked.SelectedIndex = paths.IndexOf(viewModel.SelectedParked);

        if (Parked.SelectedIndex < 0)
        {
            viewModel.SelectedParked = null;
        }
    }

    private async void ReleaseNotesClicked(object sender, RoutedEventArgs args)
    {
        if (model.UpdateService.Available is not { } available)
        {
            return;
        }
        if (available.Notes.Length > 0)
        {
            await ReleaseNotesAsync(available);
        }
        else if (available.NotesUrl is { } url)
        {
            model.Open(url);
        }
    }

    private async Task ReleaseNotesAsync(UpdateFeed.Item update)
    {
        var notes = new HtmlView(false, false, false, model.Shell) { Width = 500, Height = 380 };
        var load = notes.LoadAsync(ReleaseNotesPage(update.Notes));
        var dialog = new ContentDialog
        {
            Title = $"Flock {update.Version}",
            Content = notes,
            PrimaryButtonText = Localization.Format(Strings.General_UpdateTo, update.Version),
            SecondaryButtonText = update.NotesUrl is null ? "" : Strings.General_ViewOnGithub,
            CloseButtonText = Strings.Common_Close,
            DefaultButton = ContentDialogButton.Primary,
            XamlRoot = XamlRoot,
            Resources =
            {
                ["ContentDialogMaxWidth"] = 600.0
            }
        };

        ContentDialogResult result;
        try
        {
            result = await dialog.ShowAsync();
        }
        finally
        {
            await load;
            await notes.ReleaseAsync();
        }

        // Close and Esc give None: nothing to do then.
        if (result == ContentDialogResult.Primary)
        {
            await model.UpdateService.InstallAsync();
        }
        else if (result == ContentDialogResult.Secondary)
        {
            model.Open(update.NotesUrl!);
        }
    }

    private static string ReleaseNotesPage(string notes)
    {
        return $$"""
                 <!doctype html><html><head><meta charset="utf-8"><style>
                 :root { color-scheme: light dark; color: #1b1b1b; } @media (prefers-color-scheme: dark) { :root { color: #ffffff; } a { color: #99ebff; } }
                 html, body { background: transparent; } body { margin: 0; font: 14px/1.5 "Segoe UI Variable Text", "Segoe UI", sans-serif; }
                 a { color: #005fb8; text-decoration: none; } a:hover { text-decoration: underline; } h1, h2, h3 { font-weight: 600; margin: 1em 0 .4em; }
                 h1 { font-size: 20px; } h2 { font-size: 17px; } h3 { font-size: 15px; } body > :first-child { margin-top: 0; }
                 ul, ol { padding-left: 1.4em; } li { margin: .2em 0; } code { font: 12px Consolas, monospace; padding: .1em .3em; border-radius: 3px; background: rgba(128, 128, 128, .18); } img { max-width: 100%; }
                 </style></head><body>{{notes}}</body></html>
                 """;
    }

    private void StartupToggled(object sender, RoutedEventArgs args)
    {
        if (!loaded || sender is not ToggleSwitch toggle || toggle.IsOn == Native.StartsAtSignIn)
        {
            return;
        }
        try
        {
            Native.StartsAtSignIn = toggle.IsOn;
        }
        catch (Exception e) when (e is UnauthorizedAccessException or SecurityException or IOException)
        {
            model.ShowRuntimeError(Localization.Format(Strings.General_CouldNotChangeStartup, e.Message));
        }
    }

    private async void BetaToggled(object sender, RoutedEventArgs args)
    {
        if (!loaded || Beta.IsOn == viewModel.IsBeta)
            return;

        await viewModel.SetChannelCommand.ExecuteAsync(Beta.IsOn ? "beta" : "stable");
        await model.UpdateService.CheckAsync(userInitiated: false);
    }

    private void ShowRoot(object sender, RoutedEventArgs args)
    {
        model.Reveal(viewModel.Config?.Root ?? "");
    }

    private async void AddParkedClicked(object sender, RoutedEventArgs args)
    {
        var paths = await window.PickFolders(Strings.General_AddProjectFoldersTitle);
        if (paths.Count > 0)
        {
            await PerformAsync(async client =>
            {
                foreach (var path in paths)
                {
                    await client.ParkAsync(path);
                }
            });
        }
    }

    private async void RemoveParkedClicked(object sender, RoutedEventArgs args)
    {
        if (viewModel.SelectedParked is not { } path)
            return;

        if (!await window.Confirm(Localization.Format(Strings.General_RemoveProjectFolderQuestion, path),
                Strings.General_RemoveProjectFolderDetails, Strings.Common_Remove, true))
            return;

        viewModel.SelectedParked = null;
        await PerformAsync(client => client.UnparkAsync(path));
    }

    private async Task PerformAsync(Func<FlockClient, Task> change)
    {
        await change(model.Client);
        await model.Configuration.RefreshAsync();
    }

    public static TextBlock Footnote(string text)
    {
        var note = Ui.Caption(text);
        note.Margin = new Thickness(2, 6, 0, 0);
        return note;
    }
}
