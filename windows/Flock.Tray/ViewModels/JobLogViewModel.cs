using CommunityToolkit.Mvvm.ComponentModel;
using Flock.Kit;

namespace Flock.Tray.ViewModels;

public enum JobLogState { Loading, Missing, Empty, Loaded, Failed }

/// <summary>Owns one dialog-lifetime log read, including missing, empty, failed, and tail states.</summary>
public sealed class JobLogViewModel : ObservableObject, IAsyncDisposable
{
    private readonly Func<CancellationToken, Task<string>> load;
    private readonly Func<bool> active;
    private readonly TimeProvider timeProvider;
    private readonly int tailLines;
    private readonly CancellationTokenSource lifetime = new();
    private readonly object gate = new();
    private Task? polling;
    private string text = Strings.Common_Loading;
    private string? error;
    private JobLogState state = JobLogState.Loading;

    public JobLogViewModel(Func<CancellationToken, Task<string>> load, Func<bool> active, int tailLines = 0, TimeProvider? timeProvider = null)
    {
        this.load = load;
        this.active = active;
        this.tailLines = tailLines;
        this.timeProvider = timeProvider ?? TimeProvider.System;
    }
    public string Text => text;
    public string DisplayText => state switch
    {
        JobLogState.Loading => Strings.Common_Loading,
        JobLogState.Missing => Strings.Worktrees_Job_NoLogYet,
        JobLogState.Empty => Strings.Common_EmptyParenthesized,
        JobLogState.Failed => Strings.Worktrees_Job_CouldNotLoadShort,
        _ => text,
    };
    public string? Error => error;
    public JobLogState State => state;
    public bool CanCopy => state == JobLogState.Loaded && text.Length > 0;

    public event Action? Changed;

    public Task StartAsync()
    {
        lock (gate)
        {
            return polling ??= PollAsync();
        }
    }

    public async Task CloseAsync()
    {
        lifetime.Cancel();
        Task? task;
        lock (gate)
        {
            task = polling;
        }
        if (task is not null)
        {
            await task;
        }
    }

    public ValueTask DisposeAsync() => new(CloseAsync());

    private async Task PollAsync()
    {
        while (!lifetime.IsCancellationRequested)
        {
            try
            {
                var value = await load(lifetime.Token);
                Apply(value, null, value.Length == 0 ? JobLogState.Empty : JobLogState.Loaded);
            }
            catch (FlockServerException exception) when (exception.StatusCode == 404)
            {
                Apply("", null, JobLogState.Missing);
            }
            catch (OperationCanceledException) when (lifetime.IsCancellationRequested)
            {
                return;
            }
            catch (Exception exception)
            {
                Apply("", exception.Message, JobLogState.Failed);
            }

            if (!active())
            {
                return;
            }
            try
            {
                await Task.Delay(TimeSpan.FromSeconds(2), timeProvider, lifetime.Token);
            }
            catch (OperationCanceledException) when (lifetime.IsCancellationRequested)
            {
                return;
            }
        }
    }

    private void Apply(string value, string? failure, JobLogState nextState)
    {
        var nextText = value;
        if (tailLines > 0 && value.Length > 0)
        {
            var newline = value.EndsWith('\n');
            var lines = value.TrimEnd('\r', '\n').Split('\n').TakeLast(tailLines);
            nextText = string.Join("\n", lines) + (newline ? "\n" : "");
        }
        text = nextText;
        error = failure;
        state = nextState;
        OnPropertyChanged(nameof(Text));
        OnPropertyChanged(nameof(DisplayText));
        OnPropertyChanged(nameof(Error));
        OnPropertyChanged(nameof(State));
        OnPropertyChanged(nameof(CanCopy));
        Changed?.Invoke();
    }
}