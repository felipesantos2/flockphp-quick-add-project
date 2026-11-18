using System.ComponentModel;
using Flock.Kit;
using Microsoft.Extensions.Logging;

namespace Flock.Tray.Infrastructure;

public enum TrayNotifyOperation
{
    Add,
    Modify,
    Delete,
}


/// <summary>An icon registration; with a balloon title it also shows a notification (Windows shows it as a toast).</summary>
public readonly record struct TrayNotification(IntPtr Window, IntPtr Icon, string Tooltip, string? BalloonTitle = null, string? BalloonText = null);
public readonly record struct TrayPoint(int X, int Y);

public readonly record struct TrayNativeMessage(
    IntPtr Window,
    int Message,
    IntPtr WParam,
    IntPtr LParam,
    string? Link,
    string? Text);

/// <summary>Small raw Win32 seam used by TrayService so acquisition and cleanup can be tested without Explorer.</summary>
public interface ITrayNativeApi
{
    IntPtr ModuleHandle();
    int RegisterWindowMessage(string name);
    // A null callback result delegates the message to DefWindowProcW; zero is a handled result.
    ushort RegisterClass(string name, Func<TrayNativeMessage, IntPtr?> callback, out bool alreadyRegistered, out int lastError);
    IntPtr CreateWindow(string className, IntPtr instance, out int lastError);
    IntPtr LoadImage(string path, IntPtr window, out int lastError);
    bool Notify(TrayNotifyOperation operation, TrayNotification notification);
    bool DestroyIcon(IntPtr icon);
    bool DestroyWindow(IntPtr window);
    bool UnregisterClass(string name, IntPtr instance);
    IntPtr CreatePopupMenu();
    bool AppendMenu(IntPtr menu, bool separator, IntPtr id, string? text);
    bool SetMenuDefaultItem(IntPtr menu, int item);
    int TrackPopupMenu(IntPtr menu, IntPtr window, TrayPoint cursor);
    bool DestroyMenu(IntPtr menu);
    bool SetForegroundWindow(IntPtr window);
    TrayPoint CursorPosition();
    bool PostMessage(IntPtr window, int message);
}

/// <summary>Owns the hidden tray window, icon handle, callbacks, and Explorer re-registration.</summary>
public sealed class TrayService : ITrayIcon, IDisposable
{
    private const int WM_LBUTTONUP = 0x0202;
    private const int WM_RBUTTONUP = 0x0205;
    private const int WM_CONTEXTMENU = 0x007B;
    // A click on the icon's notification (WM_USER + 5).
    private const int NIN_BALLOONUSERCLICK = 0x0405;
    private const int WM_SETTINGCHANGE = 0x001A;
    private const int WM_COPYDATA = 0x004A;
    private const int WM_SHOW_APP = Native.WM_SHOW_APP;
    private const int ERROR_CLASS_ALREADY_EXISTS = 1410;

    private readonly ITrayNativeApi native;
    private readonly string windowClass;
    private readonly IUiDispatcher? dispatcher;
    private readonly ILogger<TrayService>? logger;
    private readonly object gate = new();
    private IntPtr window;
    private IntPtr icon;
    private IntPtr instance;
    private int taskbarCreated;
    private string? className;
    private string? iconPath;
    private string? tooltip;
    private bool classOwned;
    private bool iconAdded;
    private bool started;
    private bool disposed;
    private bool disposing;

    public TrayService(ITrayNativeApi native, IUiDispatcher? dispatcher = null, ILogger<TrayService>? logger = null,
        string windowClass = Native.TrayWindowClass)
    {
        this.native = native;
        this.windowClass = windowClass;
        this.dispatcher = dispatcher;
        this.logger = logger;
    }

    public event Action? Clicked;
    public event Action? MenuRequested;
    public event Action? ShowRequested;
    public event Action<string>? LinkReceived;
    public event Action? ThemeChanged;
    public event Action? NotificationClicked;
    public event Action<string>? Failure;

    public IntPtr Handle => window;

    /// <summary>Acquires class, hidden window, icon, and shell registration as one transaction.</summary>
    public Task StartAsync(string path, string tip)
    {
        lock (gate)
        {
            if (started)
            {
                return Task.CompletedTask;
            }
            if (disposed)
            {
                throw new ObjectDisposedException(nameof(TrayService));
            }
            try
            {
                className = windowClass;
                instance = native.ModuleHandle();
                taskbarCreated = native.RegisterWindowMessage("TaskbarCreated");
                if (taskbarCreated == 0)
                {
                    throw new InvalidOperationException("RegisterWindowMessage(TaskbarCreated) failed.");
                }

                var atom = native.RegisterClass(className, Dispatch, out var alreadyRegistered, out var registerError);
                classOwned = atom != 0;
                if (atom == 0 && (!alreadyRegistered || registerError != ERROR_CLASS_ALREADY_EXISTS))
                {
                    throw NativeFailure("RegisterClassExW", registerError);
                }

                window = native.CreateWindow(className, instance, out var windowError);
                if (window == IntPtr.Zero)
                {
                    throw NativeFailure("CreateWindowExW", windowError);
                }

                icon = native.LoadImage(path, window, out var iconError);
                if (icon == IntPtr.Zero)
                {
                    throw NativeFailure("LoadImageW", iconError);
                }

                iconPath = path;
                tooltip = tip;
                if (!Notify(TrayNotifyOperation.Add, icon, tip))
                {
                    iconPath = null;
                    tooltip = null;
                    throw new InvalidOperationException("Shell_NotifyIconW(NIM_ADD) failed.");
                }
                iconAdded = true;
                started = true;
                return Task.CompletedTask;
            }
            catch (Exception)
            {
                try
                {
                    Cleanup();
                }
                catch (Exception cleanupError)
                {
                    logger?.LogError(cleanupError, "Tray cleanup failed after startup failure.");
                }
                throw;
            }
        }
    }

    /// <summary>Loads and registers a replacement before committing it, so a failed update keeps the working icon.</summary>
    public void Update(string path, string tip)
    {
        lock (gate)
        {
            if (!started || disposed || (path == iconPath && tip == tooltip))
            {
                return;
            }

            if (path != iconPath)
            {
                var replacement = native.LoadImage(path, window, out var error);
                if (replacement == IntPtr.Zero)
                {
                    ReportFailure(NativeFailure("LoadImageW", error), "tray icon update");
                    return;
                }
                if (!Notify(TrayNotifyOperation.Modify, replacement, tip))
                {
                    native.DestroyIcon(replacement);
                    ReportFailure(new InvalidOperationException("Shell_NotifyIconW(NIM_MODIFY) failed."), "tray icon update");
                    return;
                }

                var old = icon;
                icon = replacement;
                iconPath = path;
                tooltip = tip;
                if (!native.DestroyIcon(old))
                {
                    logger?.LogError("DestroyIcon failed after committing a tray icon update.");
                }
                return;
            }

            if (Notify(TrayNotifyOperation.Modify, icon, tip))
            {
                tooltip = tip;
            }
            else
            {
                ReportFailure(new InvalidOperationException("Shell_NotifyIconW(NIM_MODIFY) failed."), "tray tooltip update");
            }
        }
    }

    /// <summary>Shows a notification from the icon; a click on it raises NotificationClicked.</summary>
    public void ShowNotification(string title, string text)
    {
        lock (gate)
        {
            if (!started || disposed)
            {
                return;
            }
            if (!native.Notify(TrayNotifyOperation.Modify, new TrayNotification(window, icon, tooltip ?? "Flock", title, text)))
            {
                ReportFailure(new InvalidOperationException("Shell_NotifyIconW(NIM_MODIFY) failed."), "tray notification");
            }
        }
    }

    public int ShowMenu(IReadOnlyList<string?> items, int defaultItem = -1)
    {
        lock (gate)
        {
            if (!started || disposed)
            {
                return -1;
            }
            var menu = native.CreatePopupMenu();
            if (menu == IntPtr.Zero)
            {
                ReportFailure(new InvalidOperationException("CreatePopupMenu failed."), "tray menu");
                return -1;
            }
            try
            {
                for (var i = 0; i < items.Count; i++)
                {
                    if (!native.AppendMenu(menu, items[i] is null, (IntPtr)(i + 1), items[i]))
                    {
                        ReportFailure(new InvalidOperationException("AppendMenu failed."), "tray menu");
                        return -1;
                    }
                }
                if (defaultItem >= 0 && !native.SetMenuDefaultItem(menu, defaultItem + 1))
                {
                    ReportFailure(new InvalidOperationException("SetMenuDefaultItem failed."), "tray menu");
                    return -1;
                }
                native.SetForegroundWindow(window);
                var chosen = native.TrackPopupMenu(menu, window, native.CursorPosition());
                native.PostMessage(window, 0);
                return chosen - 1;
            }
            finally
            {
                native.DestroyMenu(menu);
            }
        }
    }

    public void Dispose()
    {
        lock (gate)
        {
            if (disposed || disposing)
            {
                return;
            }
            disposing = true;
            try
            {
                Cleanup();
                disposed = true;
            }
            finally
            {
                disposing = false;
            }
        }
    }

    private void Cleanup()
    {
        Exception? failure = null;
        if (iconAdded)
        {
            try
            {
                if (!Notify(TrayNotifyOperation.Delete, icon, tooltip ?? "Flock"))
                {
                    logger?.LogError("Shell_NotifyIconW(NIM_DELETE) failed while releasing the tray icon.");
                }
            }
            catch (Exception error)
            {
                failure ??= error;
            }
            iconAdded = false;
        }
        if (icon != IntPtr.Zero)
        {
            try
            {
                if (!native.DestroyIcon(icon))
                {
                    logger?.LogError("DestroyIcon failed while releasing the tray icon.");
                }
            }
            catch (Exception error)
            {
                failure ??= error;
            }
            icon = IntPtr.Zero;
        }
        if (window != IntPtr.Zero)
        {
            try
            {
                if (!native.DestroyWindow(window))
                {
                    logger?.LogError("DestroyWindow failed while releasing the tray window.");
                }
            }
            catch (Exception error)
            {
                failure ??= error;
            }
            window = IntPtr.Zero;
        }
        if (classOwned && className is not null)
        {
            try
            {
                if (!native.UnregisterClass(className, instance))
                {
                    logger?.LogError("UnregisterClass failed while releasing the tray window class.");
                }
            }
            catch (Exception error)
            {
                failure ??= error;
            }
            classOwned = false;
        }
        started = false;
        iconPath = null;
        tooltip = null;
        if (failure is not null)
        {
            throw failure;
        }
    }

    private bool Notify(TrayNotifyOperation operation, IntPtr handle, string tip) =>
        native.Notify(operation, new TrayNotification(window, handle, tip));

    private IntPtr? Dispatch(TrayNativeMessage message)
    {
        try
        {
            if (message.Window != window)
            {
                return null;
            }
            if (message.Message == Native.WM_TRAY)
            {
                switch ((int)(message.LParam.ToInt64() & 0xFFFF))
                {
                    case WM_LBUTTONUP:
                        Publish(Clicked);
                        break;
                    case WM_RBUTTONUP:
                    case WM_CONTEXTMENU:
                        Publish(MenuRequested);
                        break;
                    case NIN_BALLOONUSERCLICK:
                        Publish(NotificationClicked);
                        break;
                }
                return IntPtr.Zero;
            }
            if (message.Message == WM_SHOW_APP)
            {
                Publish(ShowRequested);
                return IntPtr.Zero;
            }
            if (message.Message == WM_COPYDATA)
            {
                if (message.Link is not null)
                {
                    Publish(() => LinkReceived?.Invoke(message.Link));
                    return (IntPtr)1;
                }
                return IntPtr.Zero;
            }
            if (message.Message == WM_SETTINGCHANGE && message.Text == "ImmersiveColorSet")
            {
                Publish(ThemeChanged);
            }
            if (message.Message == taskbarCreated)
            {
                if (!Notify(TrayNotifyOperation.Add, icon, tooltip ?? "Flock"))
                {
                    ReportFailure(new InvalidOperationException("Shell_NotifyIconW(NIM_ADD) failed after Explorer recreated the taskbar."), "tray re-add");
                }
                return IntPtr.Zero;
            }
        }
        catch (Exception error)
        {
            ReportFailure(error, "tray callback");
        }
        return null;
    }

    private void Publish(Action? callback)
    {
        if (callback is null || disposed)
        {
            return;
        }
        try
        {
            if (dispatcher is null)
            {
                callback();
            }
            else
            {
                dispatcher.InvokeAsync(callback, CancellationToken.None).GetAwaiter().GetResult();
            }
        }
        catch (Exception error)
        {
            ReportFailure(error, "tray callback");
        }
    }

    private void ReportFailure(Exception error, string operation)
    {
        logger?.LogError(error, "Tray operation {Operation} failed.", operation);
        try
        {
            Failure?.Invoke(Localization.Format(Strings.Menu_Failure, operation));
        }
        catch (Exception callbackError)
        {
            logger?.LogError(callbackError, "Tray failure notification failed.");
        }
    }

    private static Win32Exception NativeFailure(string operation, int error) =>
        new(error == 0 ? 1 : error, $"{operation} failed.");
}
