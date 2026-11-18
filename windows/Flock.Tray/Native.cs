using System.Runtime.InteropServices;
using Flock.Tray.Infrastructure;
using Microsoft.Win32;

namespace Flock.Tray;

/// <summary>Win32 calls WinUI has no API for: the notification area icon, its menu, consoles.</summary>
internal static partial class Native
{
    public const int WM_APP = 0x8000;
    public const int WM_TRAY = WM_APP + 1;
    public const int WM_SHOW_APP = WM_APP + 2;
    private const int WM_NULL = 0x0000;
    private const int WM_LBUTTONUP = 0x0202;
    private const int WM_RBUTTONUP = 0x0205;
    private const int WM_CONTEXTMENU = 0x007B;
    private const int WM_SETTINGCHANGE = 0x001A;

    /// <summary>Class name of the tray icon's hidden window (also how a second instance finds the first).</summary>
    public const string TrayWindowClass = "Flock.Tray.NotifyWindow";

    [DllImport("kernel32.dll")]
    private static extern bool AttachConsole(int processId);

    /// <summary>Lets a GUI program print to the console it was started from (`FlockTray --selftest`).</summary>
    public static void AttachParentConsole() => AttachConsole(-1);

    [DllImport("user32.dll", CharSet = CharSet.Unicode)]
    private static extern IntPtr FindWindow(string className, string? windowName);

    [DllImport("user32.dll")]
    private static extern bool PostMessage(IntPtr hwnd, int msg, IntPtr wParam, IntPtr lParam);

    [DllImport("user32.dll")]
    private static extern uint GetWindowThreadProcessId(IntPtr hwnd, out uint processId);

    [DllImport("user32.dll")]
    private static extern bool AllowSetForegroundWindow(uint processId);

    private const int WM_COPYDATA = 0x004A;
    /// <summary>Marks a WM_COPYDATA from a second instance as a flock:// link ("FLK1").</summary>
    private static readonly IntPtr LinkMessage = 0x464C4B31;

    [StructLayout(LayoutKind.Sequential)]
    private struct COPYDATASTRUCT
    {
        public IntPtr dwData;
        public int cbData;
        public IntPtr lpData;
    }

    [DllImport("user32.dll", CharSet = CharSet.Unicode)]
    private static extern IntPtr SendMessageTimeout(IntPtr hwnd, int msg, IntPtr wParam, ref COPYDATASTRUCT lParam, int flags, int timeout, out IntPtr result);

    /// <summary>
    /// Asks the running instance to bring its main window to the front, or, for a flock:// link
    /// this process was started with, to open the link.
    /// </summary>
    public static void SignalRunningInstance(string? link = null, string windowClass = TrayWindowClass)
    {
        var hwnd = FindWindow(windowClass, null);
        if (hwnd != IntPtr.Zero)
        {
            // This process was just started by the user, so it may take the foreground; pass that
            // on, or the running instance's window would only flash in the taskbar.
            GetWindowThreadProcessId(hwnd, out var process);
            AllowSetForegroundWindow(process);
            if (link is null)
            {
                PostMessage(hwnd, WM_SHOW_APP, IntPtr.Zero, IntPtr.Zero);
                return;
            }
            // WM_COPYDATA carries the text across processes; it only lives until the call returns.
            var text = Marshal.StringToHGlobalUni(link);
            try
            {
                var data = new COPYDATASTRUCT { dwData = LinkMessage, cbData = (link.Length + 1) * 2, lpData = text };
                const int SMTO_ABORTIFHUNG = 0x2;
                SendMessageTimeout(hwnd, WM_COPYDATA, IntPtr.Zero, ref data, SMTO_ABORTIFHUNG, 5000, out _);
            }
            finally
            {
                Marshal.FreeHGlobal(text);
            }
        }
    }

    [StructLayout(LayoutKind.Sequential)]
    public struct POINT
    {
        public int X;
        public int Y;
    }

    [DllImport("user32.dll")]
    private static extern bool GetCursorPos(out POINT point);

    public static POINT CursorPosition() => GetCursorPos(out var p) ? p : default;

    [DllImport("user32.dll")]
    public static extern uint GetDpiForWindow(IntPtr hwnd);

    [DllImport("user32.dll")]
    public static extern bool SetForegroundWindow(IntPtr hwnd);

    [DllImport("user32.dll", CharSet = CharSet.Unicode)]
    private static extern int MessageBox(IntPtr hwnd, string text, string caption, uint type);

    /// <summary>A plain error dialog, for when WinUI itself could not start.</summary>
    public static void ShowError(string caption, string text) => MessageBox(IntPtr.Zero, text, caption, 0x10 /* MB_ICONERROR */);

    // Signatures

    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    private struct WINTRUST_FILE_INFO
    {
        public int cbStruct;
        public string pcwszFilePath;
        public IntPtr hFile;
        public IntPtr pgKnownSubject;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct WINTRUST_DATA
    {
        public int cbStruct;
        public IntPtr pPolicyCallbackData;
        public IntPtr pSIPClientData;
        public int dwUIChoice;
        public int fdwRevocationChecks;
        public int dwUnionChoice;
        public IntPtr pFile;
        public int dwStateAction;
        public IntPtr hWVTStateData;
        public IntPtr pwszURLReference;
        public int dwProvFlags;
        public int dwUIContext;
        public IntPtr pSignatureSettings;
    }

    [DllImport("wintrust.dll", CharSet = CharSet.Unicode)]
    private static extern int WinVerifyTrust(IntPtr hwnd, ref Guid action, ref WINTRUST_DATA data);

    /// <summary>
    /// Whether a file has a valid Authenticode signature whose chain Windows trusts, revocation of
    /// the whole chain checked (WinVerifyTrust, without any UI). Returns the failure code otherwise.
    /// </summary>
    public static int VerifySignature(string path)
    {
        var action = new Guid("00AAC56B-CD44-11d0-8CC2-00C04FC295EE"); // WINTRUST_ACTION_GENERIC_VERIFY_V2
        var file = new WINTRUST_FILE_INFO { cbStruct = Marshal.SizeOf<WINTRUST_FILE_INFO>(), pcwszFilePath = path };
        var filePointer = Marshal.AllocHGlobal(file.cbStruct);
        try
        {
            Marshal.StructureToPtr(file, filePointer, false);
            var data = new WINTRUST_DATA
            {
                cbStruct = Marshal.SizeOf<WINTRUST_DATA>(),
                dwUIChoice = 2,               // WTD_UI_NONE
                fdwRevocationChecks = 1,      // WTD_REVOKE_WHOLECHAIN
                dwUnionChoice = 1,            // WTD_CHOICE_FILE
                pFile = filePointer,
                dwStateAction = 1,            // WTD_STATEACTION_VERIFY
                dwProvFlags = 0x80,           // WTD_REVOCATION_CHECK_CHAIN
            };
            var result = WinVerifyTrust(new IntPtr(-1) /* INVALID_HANDLE_VALUE: no UI */, ref action, ref data);
            data.dwStateAction = 2;           // WTD_STATEACTION_CLOSE
            WinVerifyTrust(new IntPtr(-1), ref action, ref data);
            return result;
        }
        finally
        {
            Marshal.DestroyStructure<WINTRUST_FILE_INFO>(filePointer);
            Marshal.FreeHGlobal(filePointer);
        }
    }

    /// <summary>
    /// Whether the taskbar is light. It follows Windows' mode, which can differ from the apps' mode
    /// (Settings → Personalization → Colors); Windows 10's default taskbar is dark.
    /// </summary>
    public static bool TaskbarIsLight
    {
        get
        {
            using var key = Registry.CurrentUser.OpenSubKey(@"Software\Microsoft\Windows\CurrentVersion\Themes\Personalize");
            return key?.GetValue("SystemUsesLightTheme") is int value && value != 0;
        }
    }

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern bool GetUserPreferredUILanguages(uint flags, out uint count, char[]? buffer, ref uint length);

    /// <summary>
    /// The user's preferred UI languages, most preferred first ("cs-CZ", "en-US"); empty when Windows
    /// can't tell. Plain Win32, so it works before WinUI and its COM wrappers are set up.
    /// </summary>
    public static IReadOnlyList<string> PreferredUiLanguages()
    {
        const uint MUI_LANGUAGE_NAME = 0x8;
        uint length = 0;
        if (!GetUserPreferredUILanguages(MUI_LANGUAGE_NAME, out _, null, ref length) || length == 0)
        {
            return [];
        }
        var buffer = new char[length];
        if (!GetUserPreferredUILanguages(MUI_LANGUAGE_NAME, out _, buffer, ref length))
        {
            return [];
        }
        // A list of null-terminated names that ends with an empty one.
        return new string(buffer).Split('\0', StringSplitOptions.RemoveEmptyEntries);
    }

    // Start at sign-in

    private const string RunKey = @"Software\Microsoft\Windows\CurrentVersion\Run";
    private const string RunValue = "Flock";

    public static bool StartsAtSignIn
    {
        get
        {
            using var key = Registry.CurrentUser.OpenSubKey(RunKey);
            return key?.GetValue(RunValue) is string;
        }
        set
        {
            using var key = Registry.CurrentUser.CreateSubKey(RunKey);
            if (value)
            {
                key.SetValue(RunValue, $"\"{Environment.ProcessPath}\" {App.BackgroundArgument}");
            }
            else
            {
                key.DeleteValue(RunValue, throwOnMissingValue: false);
            }
        }
    }

    // Choosing folders

    /// <summary>
    /// The shell's folder dialog with several folders selectable at once (WinUI's FolderPicker picks
    /// one). An empty list when cancelled; COMException when the dialog isn't available.
    /// </summary>
    public static List<string> PickFolders(IntPtr owner, string title)
    {
        const uint FOS_PICKFOLDERS = 0x20, FOS_FORCEFILESYSTEM = 0x40, FOS_ALLOWMULTISELECT = 0x200, FOS_PATHMUSTEXIST = 0x800;
        const uint SIGDN_FILESYSPATH = 0x80058000;
        const int ERROR_CANCELLED = unchecked((int)0x800704C7);

        var dialog = (IFileOpenDialog)new FileOpenDialog();
        try
        {
            dialog.GetOptions(out var options);
            dialog.SetOptions(options | FOS_PICKFOLDERS | FOS_FORCEFILESYSTEM | FOS_ALLOWMULTISELECT | FOS_PATHMUSTEXIST);
            dialog.SetTitle(title);
            var hr = dialog.Show(owner);
            if (hr == ERROR_CANCELLED)
            {
                return [];
            }
            Marshal.ThrowExceptionForHR(hr);

            dialog.GetResults(out var items);
            items.GetCount(out var count);
            var paths = new List<string>();
            for (uint i = 0; i < count; i++)
            {
                items.GetItemAt(i, out var item);
                item.GetDisplayName(SIGDN_FILESYSPATH, out var path);
                paths.Add(path);
            }
            return paths;
        }
        finally
        {
            Marshal.ReleaseComObject(dialog);
        }
    }

    [ComImport, Guid("DC1C5A9C-E88A-4dde-A5A1-60F82A20AEF7")]
    private class FileOpenDialog;

    // Vtable order matters: IModalWindow, then IFileDialog, then IFileOpenDialog (shobjidl_core.h).
    [ComImport, Guid("d57c7288-d4ad-4768-be02-9d969532d960"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    private interface IFileOpenDialog
    {
        [PreserveSig] int Show(IntPtr parent);
        void SetFileTypes(uint count, IntPtr filterSpec);
        void SetFileTypeIndex(uint index);
        void GetFileTypeIndex(out uint index);
        void Advise(IntPtr events, out uint cookie);
        void Unadvise(uint cookie);
        void SetOptions(uint options);
        void GetOptions(out uint options);
        void SetDefaultFolder(IShellItem item);
        void SetFolder(IShellItem item);
        void GetFolder(out IShellItem item);
        void GetCurrentSelection(out IShellItem item);
        void SetFileName([MarshalAs(UnmanagedType.LPWStr)] string name);
        void GetFileName([MarshalAs(UnmanagedType.LPWStr)] out string name);
        void SetTitle([MarshalAs(UnmanagedType.LPWStr)] string title);
        void SetOkButtonLabel([MarshalAs(UnmanagedType.LPWStr)] string text);
        void SetFileNameLabel([MarshalAs(UnmanagedType.LPWStr)] string label);
        void GetResult(out IShellItem item);
        void AddPlace(IShellItem item, int place);
        void SetDefaultExtension([MarshalAs(UnmanagedType.LPWStr)] string extension);
        void Close(int hr);
        void SetClientGuid(ref Guid guid);
        void ClearClientData();
        void SetFilter(IntPtr filter);
        void GetResults(out IShellItemArray items);
        void GetSelectedItems(out IShellItemArray items);
    }

    [ComImport, Guid("43826d1e-e718-42ee-bc55-a1e261c37bfe"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    private interface IShellItem
    {
        void BindToHandler(IntPtr bindContext, ref Guid handler, ref Guid riid, out IntPtr result);
        void GetParent(out IShellItem parent);
        void GetDisplayName(uint form, [MarshalAs(UnmanagedType.LPWStr)] out string name);
        void GetAttributes(uint mask, out uint attributes);
        void Compare(IShellItem other, uint hint, out int order);
    }

    [ComImport, Guid("b63ea76d-1f85-456f-a19c-48159efa858b"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    private interface IShellItemArray
    {
        void BindToHandler(IntPtr bindContext, ref Guid handler, ref Guid riid, out IntPtr result);
        void GetPropertyStore(int flags, ref Guid riid, out IntPtr result);
        void GetPropertyDescriptionList(IntPtr keyType, ref Guid riid, out IntPtr result);
        void GetAttributes(int flags, uint mask, out uint attributes);
        void GetCount(out uint count);
        void GetItemAt(uint index, out IShellItem item);
        void EnumItems(out IntPtr items);
    }

    // Notification area icon. The orchestration and failure policy live in Infrastructure/TrayService;
    // this file keeps only the raw interop and the callback pointer rooted for the native class lifetime.

    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    private struct NOTIFYICONDATAW
    {
        public int cbSize;
        public IntPtr hWnd;
        public int uID;
        public int uFlags;
        public int uCallbackMessage;
        public IntPtr hIcon;
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 128)]
        public string szTip;
        public int dwState;
        public int dwStateMask;
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 256)]
        public string szInfo;
        public int uVersion;
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 64)]
        public string szInfoTitle;
        public int dwInfoFlags;
        public Guid guidItem;
        public IntPtr hBalloonIcon;
    }

    private const int NIF_MESSAGE = 0x1, NIF_ICON = 0x2, NIF_TIP = 0x4, NIF_INFO = 0x10;
    // No icon of its own (the toast shows the app's), and none while Focus or a full-screen app is on.
    private const int NIIF_NONE = 0x0, NIIF_RESPECT_QUIET_TIME = 0x80;
    private const int IMAGE_ICON = 1, LR_LOADFROMFILE = 0x10, SM_CXSMICON = 49;
    private const int ERROR_CLASS_ALREADY_EXISTS = 1410;

    [DllImport("shell32.dll", CharSet = CharSet.Unicode)]
    private static extern bool Shell_NotifyIcon(int message, ref NOTIFYICONDATAW data);

    private delegate IntPtr WndProc(IntPtr hwnd, int msg, IntPtr wParam, IntPtr lParam);

    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    private struct WNDCLASSEXW
    {
        public int cbSize;
        public int style;
        public IntPtr lpfnWndProc;
        public int cbClsExtra;
        public int cbWndExtra;
        public IntPtr hInstance;
        public IntPtr hIcon;
        public IntPtr hCursor;
        public IntPtr hbrBackground;
        public string? lpszMenuName;
        public string lpszClassName;
        public IntPtr hIconSm;
    }

    [DllImport("user32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern ushort RegisterClassEx(ref WNDCLASSEXW wc);

    [DllImport("user32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern IntPtr CreateWindowEx(int exStyle, string className, string windowName, int style,
        int x, int y, int width, int height, IntPtr parent, IntPtr menu, IntPtr instance, IntPtr param);

    [DllImport("user32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern bool UnregisterClass(string className, IntPtr instance);

    [DllImport("user32.dll")]
    private static extern bool DestroyWindow(IntPtr hwnd);

    [DllImport("user32.dll", CharSet = CharSet.Unicode)]
    private static extern IntPtr DefWindowProc(IntPtr hwnd, int msg, IntPtr wParam, IntPtr lParam);

    [DllImport("user32.dll", CharSet = CharSet.Unicode)]
    private static extern int RegisterWindowMessage(string name);

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode)]
    private static extern IntPtr GetModuleHandle(string? name);

    [DllImport("user32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern IntPtr LoadImage(IntPtr instance, string name, int type, int cx, int cy, int flags);

    [DllImport("user32.dll")]
    private static extern bool DestroyIcon(IntPtr icon);

    [DllImport("user32.dll")]
    private static extern int GetSystemMetricsForDpi(int index, uint dpi);

    [DllImport("user32.dll")]
    private static extern IntPtr CreatePopupMenu();

    [DllImport("user32.dll", CharSet = CharSet.Unicode)]
    private static extern bool AppendMenu(IntPtr menu, int flags, IntPtr id, string? text);

    [DllImport("user32.dll")]
    private static extern int TrackPopupMenuEx(IntPtr menu, int flags, int x, int y, IntPtr hwnd, IntPtr tpm);

    [DllImport("user32.dll")]
    private static extern bool DestroyMenu(IntPtr menu);

    [DllImport("user32.dll")]
    private static extern bool SetMenuDefaultItem(IntPtr menu, int item, int byPosition);

    private static readonly object trayGate = new();
    private static WndProc? trayProc; // kept alive while Windows can call the class procedure
    private static Func<TrayNativeMessage, IntPtr?>? trayCallback;

    internal static IntPtr ModuleHandle() => GetModuleHandle(null);

    internal static ushort RegisterTrayClass(string className, Func<TrayNativeMessage, IntPtr?> callback, out bool alreadyRegistered, out int error)
    {
        lock (trayGate)
        {
            trayCallback = callback;
            trayProc = TrayDispatch;
            var wc = new WNDCLASSEXW
            {
                cbSize = Marshal.SizeOf<WNDCLASSEXW>(),
                lpfnWndProc = Marshal.GetFunctionPointerForDelegate(trayProc),
                hInstance = ModuleHandle(),
                lpszClassName = className,
            };
            var atom = RegisterClassEx(ref wc);
            error = Marshal.GetLastWin32Error();
            alreadyRegistered = atom == 0 && error == ERROR_CLASS_ALREADY_EXISTS;
            return atom;
        }
    }

    internal static bool UnregisterTrayClass(string className, IntPtr instance)
    {
        lock (trayGate)
        {
            var result = UnregisterClass(className, instance);
            if (result)
            {
                trayCallback = null;
                trayProc = null;
            }
            return result;
        }
    }

    internal static IntPtr CreateTrayWindow(string className, IntPtr instance, out int error)
    {
        var hwnd = CreateWindowEx(0, className, "Flock", 0, 0, 0, 0, 0, IntPtr.Zero, IntPtr.Zero, instance, IntPtr.Zero);
        error = Marshal.GetLastWin32Error();
        return hwnd;
    }

    internal static int RegisterTrayWindowMessage(string name) => RegisterWindowMessage(name);

    internal static IntPtr LoadTrayIcon(string path, IntPtr hwnd, out int error)
    {
        var dpi = hwnd == IntPtr.Zero ? 96u : Math.Max(96u, GetDpiForWindow(hwnd));
        var size = GetSystemMetricsForDpi(SM_CXSMICON, dpi);
        var icon = LoadImage(IntPtr.Zero, path, IMAGE_ICON, size, size, LR_LOADFROMFILE);
        error = Marshal.GetLastWin32Error();
        return icon;
    }

    internal static bool NotifyTray(TrayNotifyOperation operation, TrayNotification notification)
    {
        var data = new NOTIFYICONDATAW
        {
            cbSize = Marshal.SizeOf<NOTIFYICONDATAW>(),
            hWnd = notification.Window,
            uID = 1,
            uFlags = NIF_MESSAGE | NIF_ICON | NIF_TIP,
            uCallbackMessage = WM_TRAY,
            hIcon = notification.Icon,
            szTip = notification.Tooltip.Length > 127 ? notification.Tooltip[..127] : notification.Tooltip,
            szInfo = "",
            szInfoTitle = "",
        };
        if (notification.BalloonTitle is { } title)
        {
            var text = notification.BalloonText ?? "";
            data.uFlags |= NIF_INFO;
            data.szInfoTitle = title.Length > 63 ? title[..63] : title;
            data.szInfo = text.Length > 255 ? text[..255] : text;
            data.dwInfoFlags = NIIF_NONE | NIIF_RESPECT_QUIET_TIME;
        }
        return Shell_NotifyIcon((int)operation, ref data);
    }

    internal static bool DestroyTrayIcon(IntPtr icon) => DestroyIcon(icon);

    internal static bool DestroyTrayWindow(IntPtr hwnd) => DestroyWindow(hwnd);

    internal static IntPtr CreateTrayPopupMenu() => CreatePopupMenu();

    internal static bool AppendTrayMenu(IntPtr menu, bool separator, IntPtr id, string? text)
    {
        const int MF_STRING = 0x0, MF_SEPARATOR = 0x800;
        return AppendMenu(menu, separator ? MF_SEPARATOR : MF_STRING, id, text);
    }

    internal static bool SetTrayMenuDefaultItem(IntPtr menu, int item) => SetMenuDefaultItem(menu, item, 0);

    internal static int TrackTrayPopupMenu(IntPtr menu, IntPtr hwnd, POINT cursor)
    {
        const int TPM_RETURNCMD = 0x100, TPM_RIGHTBUTTON = 0x2, TPM_BOTTOMALIGN = 0x20;
        return TrackPopupMenuEx(menu, TPM_RETURNCMD | TPM_RIGHTBUTTON | TPM_BOTTOMALIGN, cursor.X, cursor.Y, hwnd, IntPtr.Zero);
    }

    internal static bool DestroyTrayMenu(IntPtr menu) => DestroyMenu(menu);

    internal static bool SetTrayForegroundWindow(IntPtr hwnd) => SetForegroundWindow(hwnd);

    internal static bool PostTrayMessage(IntPtr hwnd, int message) => PostMessage(hwnd, message, IntPtr.Zero, IntPtr.Zero);

    private static IntPtr TrayDispatch(IntPtr hwnd, int msg, IntPtr wParam, IntPtr lParam)
    {
        try
        {
            string? link = null;
            string? text = null;
            if (msg == WM_COPYDATA)
            {
                var data = Marshal.PtrToStructure<COPYDATASTRUCT>(lParam);
                if (data.dwData == LinkMessage && data.cbData is > 0 and <= 1024)
                {
                    link = Marshal.PtrToStringUni(data.lpData, data.cbData / 2)?.TrimEnd('\0');
                }
            }
            else if (msg == WM_SETTINGCHANGE)
            {
                text = Marshal.PtrToStringUni(lParam);
            }
            var callback = trayCallback;
            return callback?.Invoke(new TrayNativeMessage(hwnd, msg, wParam, lParam, link, text))
                ?? DefWindowProc(hwnd, msg, wParam, lParam);
        }
        catch
        {
            // A native window procedure must not let managed exceptions cross the ABI boundary.
            return DefWindowProc(hwnd, msg, wParam, lParam);
        }
    }
}
