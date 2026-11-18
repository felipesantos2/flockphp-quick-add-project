using System.Diagnostics;
using System.Text;
using Flock.Kit;

namespace Flock.Tray.Infrastructure;

public sealed record CliProcessResult(bool Started, int ExitCode, string Output, bool Canceled = false)
{
    public bool Succeeded => Started && !Canceled && ExitCode == 0;
}

/// <summary>Runs a child process without a shell and owns cancellation of children it started.</summary>
public class CliProcessRunner
{
    public virtual bool LaunchDetached(string fileName, IReadOnlyList<string> arguments)
    {
        var start = new ProcessStartInfo(fileName) { UseShellExecute = true };
        foreach (var argument in arguments)
        {
            start.ArgumentList.Add(argument);
        }
        try
        {
            using var process = Process.Start(start);
            return process is not null;
        }
        catch (Exception error) when (error is System.ComponentModel.Win32Exception or InvalidOperationException)
        {
            return false;
        }
    }

    public virtual async Task<CliProcessResult> RunAsync(string fileName, IReadOnlyList<string> arguments, TimeSpan? deadline = null, CancellationToken cancellationToken = default)
    {
        var start = new ProcessStartInfo(fileName)
        {
            CreateNoWindow = true,
            UseShellExecute = false,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
            StandardOutputEncoding = Encoding.UTF8,
            StandardErrorEncoding = Encoding.UTF8,
        };
        foreach (var argument in arguments)
        {
            start.ArgumentList.Add(argument);
        }

        using var process = new Process { StartInfo = start, EnableRaisingEvents = true };
        try
        {
            if (!process.Start())
            {
                return new CliProcessResult(false, -1, Strings.App_DidNotStart);
            }
        }
        catch (Exception error) when (error is System.ComponentModel.Win32Exception or InvalidOperationException)
        {
            return new CliProcessResult(false, -1, Localization.Format(Strings.App_CouldNotRun, fileName, error.Message));
        }

        using var deadlineCts = deadline is { } limit ? new CancellationTokenSource(limit) : null;
        using var stop = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken, deadlineCts?.Token ?? CancellationToken.None);
        try
        {
            var stdout = process.StandardOutput.ReadToEndAsync(stop.Token);
            var stderr = process.StandardError.ReadToEndAsync(stop.Token);
            await process.WaitForExitAsync(stop.Token);
            var output = (await stdout + await stderr).Trim();
            return new CliProcessResult(true, process.ExitCode, output);
        }
        catch (OperationCanceledException) when (stop.IsCancellationRequested)
        {
            try
            {
                process.Kill(entireProcessTree: true);
            }
            catch (InvalidOperationException)
            {
            }
            catch (System.ComponentModel.Win32Exception)
            {
            }
            try
            {
                await process.WaitForExitAsync();
            }
            catch (InvalidOperationException)
            {
            }
            return new CliProcessResult(true, -1, "", Canceled: true);
        }
    }
}