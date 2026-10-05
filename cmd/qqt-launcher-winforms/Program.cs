using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Drawing;
using System.IO;
using System.Linq;
using System.Net;
using System.Net.NetworkInformation;
using System.Net.Sockets;
using System.Text;
using System.Threading.Tasks;
using System.Web.Script.Serialization;
using System.Windows.Forms;

namespace QQTangLauncher
{
    internal sealed class NetworkSettings
    {
        public int schema_version { get; set; }
        public string mode { get; set; }
        public string server_ip { get; set; }
        public string client_server_ip { get; set; }
        public bool gm_remote { get; set; }
    }

    internal sealed class CoreStatus
    {
        public bool server_running { get; set; }
        public int server_pid { get; set; }
        public int client_count { get; set; }
        public NetworkSettings settings { get; set; }
        public string gm_url { get; set; }
    }

    internal sealed class CoreResponse
    {
        public bool ok { get; set; }
        public string message { get; set; }
        public CoreStatus status { get; set; }
        public string[] ip_candidates { get; set; }
        public string diagnostic_path { get; set; }
    }

    internal sealed class CoreCallResult
    {
        public CoreResponse Response;
        public string Error;
    }

    internal static class Program
    {
        [STAThread]
        private static void Main()
        {
            Application.EnableVisualStyles();
            Application.SetCompatibleTextRenderingDefault(false);
            Application.Run(new LauncherForm());
        }
    }

    internal sealed class LauncherForm : Form
    {
        private static readonly Color Primary = Color.FromArgb(20, 148, 211);
        private static readonly Color PrimaryDark = Color.FromArgb(11, 103, 159);
        private static readonly Color SoftBlue = Color.FromArgb(230, 247, 255);
        private static readonly Color TextColor = Color.FromArgb(23, 55, 78);
        private static readonly Color Muted = Color.FromArgb(77, 112, 134);
        private static readonly Color Danger = Color.FromArgb(222, 78, 78);
        private static readonly Color Success = Color.FromArgb(42, 157, 100);
        private static readonly Color Stopped = Color.FromArgb(118, 134, 145);

        private readonly string root;
        private readonly string corePath;
        private readonly JavaScriptSerializer serializer = new JavaScriptSerializer();
        private readonly Timer statusTimer = new Timer();
        private bool busy;
        private bool statusPending;

        private ComboBox mode;
        private ComboBox serverIP;
        private CheckBox gmRemote;
        private Label serverHelp;
        private Label serverState;
        private Label serverAddress;
        private TextBox clientIP;
        private Label clientState;
        private RadioButton fps45;
        private RadioButton fps60;
        private RadioButton fps144;
        private RadioButton fps300;
        private CheckBox showFPS;
        private TextBox gmUser;
        private TextBox gmPass;
        private Button saveGM;
        private Label gmURL;
        private TextBox status;

        public LauncherForm()
        {
            root = AppDomain.CurrentDomain.BaseDirectory.TrimEnd(Path.DirectorySeparatorChar);
            corePath = Path.Combine(root, "runtime", "bin", "qqt-launcher-core.exe");
            BuildUI();
            PopulateIPCandidates(null);
            Shown += async delegate
            {
                if (!File.Exists(corePath))
                {
                    ShowFailure("启动器组件缺失：" + corePath);
                    return;
                }
                await RefreshStatus(true);
                statusTimer.Interval = 1500;
                statusTimer.Tick += async delegate { await RefreshStatus(false); };
                statusTimer.Start();
            };
            FormClosing += delegate { statusTimer.Stop(); };
        }

        private void BuildUI()
        {
            Text = "QQ堂怀旧版启动器";
            StartPosition = FormStartPosition.CenterScreen;
            ClientSize = new Size(1000, 945);
            MinimumSize = new Size(1016, 984);
            Font = new Font("Microsoft YaHei UI", 10F);
            BackColor = Color.FromArgb(244, 249, 252);
            // Controls are created dynamically below. Keep autoscaling disabled
            // until the entire tree exists; otherwise WinForms scales the form
            // first and leaves subsequently added controls at 96-DPI sizes.
            AutoScaleMode = AutoScaleMode.None;
            try { Icon = Icon.ExtractAssociatedIcon(Application.ExecutablePath); } catch { }

            var accent = new Panel { Location = new Point(0, 0), Size = new Size(8, 945), Anchor = AnchorStyles.Top | AnchorStyles.Bottom | AnchorStyles.Left, BackColor = Primary };
            Controls.Add(accent);
            Controls.Add(new Label { Text = "QQ堂怀旧版", Font = new Font("Microsoft YaHei UI", 20F, FontStyle.Bold), ForeColor = PrimaryDark, AutoSize = true, Location = new Point(28, 18) });
            var sourceLink = new LinkLabel
            {
                Text = "本项目永久免费 · 从 GitHub 获取",
                Font = new Font("Microsoft YaHei UI", 10F, FontStyle.Bold),
                LinkColor = Color.FromArgb(220, 91, 48),
                ActiveLinkColor = Danger,
                AutoSize = true,
                Location = new Point(300, 30)
            };
            sourceLink.LinkClicked += delegate
            {
                try { Process.Start(new ProcessStartInfo("https://github.com/kuuhaku1314/qqtang") { UseShellExecute = true }); }
                catch (Exception ex) { ShowFailure("打开项目主页失败：" + ex.Message); }
            };
            Controls.Add(sourceLink);
            Controls.Add(new Label { Text = "服务端和客户端独立启停；适用于 Windows 10 / 11。", ForeColor = Muted, AutoSize = true, Location = new Point(31, 68) });

            var serverGroup = Group("服务端", new Point(28, 105), new Size(944, 225));
            serverState = LabelAt("● 未运行", 820, 32, 0, true, Stopped);
            serverGroup.Controls.Add(serverState);
            serverGroup.Controls.Add(LabelAt("监听模式", 20, 39));
            mode = new ComboBox { DropDownStyle = ComboBoxStyle.DropDownList, Location = new Point(125, 30), Size = new Size(270, 34) };
            mode.Items.AddRange(new object[] { "仅本机", "局域网", "公网部署" });
            mode.SelectedIndex = 0;
            mode.SelectedIndexChanged += delegate { UpdateMode(); };
            serverGroup.Controls.Add(mode);
            serverGroup.Controls.Add(LabelAt("公布 IP", 20, 87));
            serverIP = new ComboBox { DropDownStyle = ComboBoxStyle.DropDown, Location = new Point(125, 78), Size = new Size(270, 34) };
            serverIP.TextChanged += delegate { UpdateAddressText(); };
            serverGroup.Controls.Add(serverIP);
            serverHelp = LabelAt("", 440, 73, 460, false, Muted);
            serverHelp.Size = new Size(460, 60);
            serverGroup.Controls.Add(serverHelp);
            gmRemote = new CheckBox { Text = "允许远程访问 GM 后台", Location = new Point(20, 127), Size = new Size(300, 32) };
            gmRemote.CheckedChanged += delegate { UpdateRemoteGMControls(); };
            serverGroup.Controls.Add(gmRemote);

            var startServer = ButtonAt("启动服务端", 20, 170, 165, 42, Primary, Color.White);
            var stopServer = ButtonAt("停止服务端", 195, 170, 165, 42, Danger, Color.White);
            var copyServer = ButtonAt("复制服务器地址", 370, 170, 175, 42, SoftBlue, PrimaryDark);
            startServer.Click += async delegate { await StartServer(); };
            stopServer.Click += async delegate { await RunOperation("停止 QQ堂服务端", "-action", "stop-server"); };
            copyServer.Click += delegate { if (!String.IsNullOrWhiteSpace(serverIP.Text)) Clipboard.SetText(serverIP.Text.Trim()); };
            serverGroup.Controls.AddRange(new Control[] { startServer, stopServer, copyServer });
            serverAddress = LabelAt("客户端使用：127.0.0.1", 600, 180, 0, false, PrimaryDark);
            serverGroup.Controls.Add(serverAddress);

            var clientGroup = Group("游戏客户端", new Point(28, 342), new Size(944, 220));
            clientState = LabelAt("● 0 个运行中", 810, 38, 0, true, Stopped);
            clientGroup.Controls.Add(clientState);
            clientGroup.Controls.Add(LabelAt("服务器地址", 20, 47));
            clientIP = new TextBox { Location = new Point(145, 38), Size = new Size(330, 34), Text = "127.0.0.1" };
            clientGroup.Controls.Add(clientIP);
            var useServerIP = ButtonAt("使用上方地址", 490, 36, 140, 38, SoftBlue, PrimaryDark);
            useServerIP.Click += delegate { clientIP.Text = serverIP.Text.Trim(); };
            clientGroup.Controls.Add(useServerIP);
            clientGroup.Controls.Add(LabelAt("最大帧率", 20, 92));
            fps45 = FrameRateOption("45", 145, 83, false);
            fps60 = FrameRateOption("60", 215, 83, false);
            fps144 = FrameRateOption("144", 285, 83, true);
            fps300 = FrameRateOption("300", 370, 83, false);
            showFPS = new CheckBox { Text = "显示 FPS", Location = new Point(475, 82), Size = new Size(125, 32), Checked = false };
            clientGroup.Controls.AddRange(new Control[] { fps45, fps60, fps144, fps300, showFPS });
            var startClient = ButtonAt("启动一个客户端", 20, 150, 190, 42, Primary, Color.White);
            var stopClients = ButtonAt("停止所有客户端", 220, 150, 190, 42, Danger, Color.White);
            startClient.Click += async delegate
            {
                var target = clientIP.Text.Trim();
                IPAddress parsed;
                var targetIsIPv4 = IPAddress.TryParse(target, out parsed) && parsed.AddressFamily == AddressFamily.InterNetwork;
                var targetIsDNSName = !targetIsIPv4 && Uri.CheckHostName(target) == UriHostNameType.Dns && target.Contains(".");
                if (!targetIsIPv4 && !targetIsDNSName)
                {
                    ShowFailure("服务器地址必须是有效的 IPv4 地址或域名。");
                    return;
                }
                await RunOperation(
                    "启动 QQ堂客户端", "-action", "start-client", "-server-ip", target,
                    "-max-fps", SelectedFrameRate().ToString(), "-show-fps=" + showFPS.Checked.ToString().ToLowerInvariant());
            };
            stopClients.Click += async delegate { await RunOperation("停止所有 QQ堂客户端", "-action", "stop-clients"); };
            clientGroup.Controls.AddRange(new Control[] { startClient, stopClients });
            var multiHelp = LabelAt("需要多开时重复点击“启动一个客户端”；\r\n每次启动可选择不同帧率。", 465, 143, 450, false, Muted);
            multiHelp.Size = new Size(450, 65);
            clientGroup.Controls.Add(multiHelp);

            var gmGroup = Group("GM 后台", new Point(28, 574), new Size(944, 130));
            gmGroup.Controls.Add(LabelAt("远程账号", 20, 42));
            gmUser = new TextBox { Location = new Point(150, 33), Size = new Size(140, 34), Text = "admin" };
            gmGroup.Controls.Add(gmUser);
            gmGroup.Controls.Add(LabelAt("远程密码", 315, 42));
            gmPass = new TextBox { Location = new Point(410, 33), Size = new Size(165, 34), UseSystemPasswordChar = true };
            gmGroup.Controls.Add(gmPass);
            saveGM = ButtonAt("保存密码", 600, 31, 165, 38, SoftBlue, PrimaryDark);
            saveGM.Click += async delegate { await SaveGM(); };
            gmGroup.Controls.Add(saveGM);
            gmURL = LabelAt("本机地址：http://127.0.0.1:18100/gm/（本机访问无需密码）", 20, 90, 0, false, PrimaryDark);
            gmGroup.Controls.Add(gmURL);
            var openGM = ButtonAt("打开 GM", 810, 80, 105, 38, PrimaryDark, Color.White);
            openGM.Click += delegate
            {
                try { Process.Start(new ProcessStartInfo(gmURL.Tag as string ?? "http://127.0.0.1:18100/gm/") { UseShellExecute = true }); }
                catch (Exception ex) { ShowFailure("打开 GM 失败：" + ex.Message); }
            };
            gmGroup.Controls.Add(openGM);

            Controls.Add(LabelAt("状态与诊断", 30, 723, 0, true, PrimaryDark));
            Controls.Add(LabelAt("错误会写入 runtime\\logs\\launcher-ui.log", 480, 723, 0, false, Muted));
            status = new TextBox
            {
                Location = new Point(28, 757),
                Size = new Size(944, 146),
                Multiline = true,
                ReadOnly = true,
                ScrollBars = ScrollBars.Vertical,
                BackColor = Color.White,
                ForeColor = TextColor,
                BorderStyle = BorderStyle.FixedSingle,
                Anchor = AnchorStyles.Top | AnchorStyles.Bottom | AnchorStyles.Left | AnchorStyles.Right
            };
            Controls.Add(status);
            Controls.Add(LabelAt("详细日志：runtime\\logs\\launcher-ui.log；服务端日志：runtime\\logs\\local-server.stderr.log", 28, 915, 0, false, Muted));

            UpdateMode();
            UpdateRemoteGMControls();
            AutoScaleDimensions = new SizeF(96F, 96F);
            AutoScaleMode = AutoScaleMode.Dpi;
            PerformAutoScale();
        }

        private GroupBox Group(string title, Point location, Size size)
        {
            var group = new GroupBox { Text = "  " + title + "  ", Location = location, Size = size, BackColor = Color.White, ForeColor = TextColor };
            Controls.Add(group);
            return group;
        }

        private Label LabelAt(string text, int x, int y, int width = 0, bool bold = false, Color? color = null)
        {
            var label = new Label { Text = text, Location = new Point(x, y), AutoSize = width == 0, ForeColor = color ?? TextColor };
            if (width > 0) label.Size = new Size(width, 28);
            if (bold) label.Font = new Font("Microsoft YaHei UI", 10F, FontStyle.Bold);
            return label;
        }

        private Button ButtonAt(string text, int x, int y, int width, int height, Color back, Color fore)
        {
            var button = new Button
            {
                Text = text,
                Location = new Point(x, y),
                Size = new Size(width, height),
                FlatStyle = FlatStyle.Flat,
                BackColor = back,
                ForeColor = fore,
                Cursor = Cursors.Hand,
                UseVisualStyleBackColor = false
            };
            button.FlatAppearance.BorderSize = 0;
            return button;
        }

        private RadioButton FrameRateOption(string text, int x, int y, bool selected)
        {
            return new RadioButton
            {
                Text = text,
                Location = new Point(x, y),
                Size = new Size(text.Length > 2 ? 80 : 65, 32),
                Checked = selected,
                AutoCheck = true
            };
        }

        private int SelectedFrameRate()
        {
            if (fps45.Checked) return 45;
            if (fps60.Checked) return 60;
            if (fps300.Checked) return 300;
            return 144;
        }

        private void PopulateIPCandidates(IEnumerable<string> values)
        {
            var current = serverIP == null ? "" : serverIP.Text;
            var candidates = new SortedSet<string>(StringComparer.OrdinalIgnoreCase);
            if (values != null) foreach (var value in values) if (!String.IsNullOrWhiteSpace(value)) candidates.Add(value);
            try
            {
                foreach (var network in NetworkInterface.GetAllNetworkInterfaces())
                {
                    if (network.OperationalStatus != OperationalStatus.Up || network.NetworkInterfaceType == NetworkInterfaceType.Loopback) continue;
                    foreach (var address in network.GetIPProperties().UnicastAddresses)
                    {
                        if (address.Address.AddressFamily == AddressFamily.InterNetwork && IsPrivate(address.Address)) candidates.Add(address.Address.ToString());
                    }
                }
            }
            catch { }
            serverIP.Items.Clear();
            foreach (var candidate in candidates) serverIP.Items.Add(candidate);
            if (!String.IsNullOrWhiteSpace(current)) serverIP.Text = current;
        }

        private static bool IsPrivate(IPAddress address)
        {
            var b = address.GetAddressBytes();
            return b.Length == 4 && (b[0] == 10 || (b[0] == 172 && b[1] >= 16 && b[1] <= 31) || (b[0] == 192 && b[1] == 168));
        }

        private void UpdateMode()
        {
            if (mode.SelectedIndex == 0)
            {
                serverIP.Text = "127.0.0.1";
                serverIP.Enabled = false;
                serverHelp.Text = "只允许本机连接\r\n监听：127.0.0.1";
                gmRemote.Checked = false;
                gmRemote.Enabled = false;
            }
            else if (mode.SelectedIndex == 1)
            {
                serverIP.Enabled = true;
                if (!IsPrivateText(serverIP.Text) && serverIP.Items.Count > 0) serverIP.Text = serverIP.Items[0].ToString();
                serverHelp.Text = "监听所有网卡：0.0.0.0\r\n请选择好友可访问的局域网 IP";
                gmRemote.Enabled = true;
            }
            else
            {
                serverIP.Enabled = true;
                if (IsPrivateText(serverIP.Text) || serverIP.Text.Trim() == "127.0.0.1") serverIP.Text = "";
                serverHelp.Text = "监听所有网卡：0.0.0.0\r\n填写公网 IP 或域名，并自行开放端口";
                gmRemote.Enabled = true;
            }
            UpdateAddressText();
            UpdateRemoteGMControls();
        }

        private void UpdateRemoteGMControls()
        {
            var enabled = gmRemote != null && gmRemote.Enabled && gmRemote.Checked;
            if (gmUser != null) gmUser.Enabled = enabled;
            if (gmPass != null) gmPass.Enabled = enabled;
            if (saveGM != null) saveGM.Enabled = enabled;
        }

        private bool IsPrivateText(string value)
        {
            IPAddress address;
            return IPAddress.TryParse(value.Trim(), out address) && IsPrivate(address);
        }

        private string ModeValue()
        {
            return mode.SelectedIndex == 1 ? "lan-host" : mode.SelectedIndex == 2 ? "remote-host" : "local";
        }

        private void UpdateAddressText()
        {
            if (serverAddress != null) serverAddress.Text = "客户端使用：" + (String.IsNullOrWhiteSpace(serverIP.Text) ? "尚未填写" : serverIP.Text.Trim());
        }

        private async Task StartServer()
        {
            var publish = serverIP.Text.Trim();
            IPAddress parsed;
            var isIPv4 = IPAddress.TryParse(publish, out parsed) && parsed.AddressFamily == AddressFamily.InterNetwork;
            var isDNSName = Uri.CheckHostName(publish) == UriHostNameType.Dns;
            if (!isIPv4 && !(ModeValue() == "remote-host" && isDNSName))
            {
                ShowFailure(ModeValue() == "remote-host" ? "公网公布地址必须是有效的 IPv4 地址或域名。" : "公布 IP 必须是有效的 IPv4 地址。");
                return;
            }
            var arguments = new List<string> { "-action", "start-server", "-mode", ModeValue(), "-server-ip", publish, "-client-ip", publish };
            if (gmRemote.Checked) arguments.Add("-gm-remote");
            await RunOperation("启动 QQ堂服务端", arguments.ToArray());
        }

        private async Task SaveGM()
        {
            if (String.IsNullOrWhiteSpace(gmUser.Text))
            {
                ShowFailure("远程 GM 用户名不能为空。");
                return;
            }
            if (String.IsNullOrEmpty(gmPass.Text))
            {
                ShowFailure("远程 GM 密码不能为空。");
                return;
            }
            await RunOperation("保存远程 GM 密码", new[] { "-action", "save-gm", "-username", gmUser.Text.Trim(), "-password-stdin" }, gmPass.Text + Environment.NewLine);
        }

        private async Task RunOperation(string title, params string[] arguments)
        {
            await RunOperation(title, arguments, null);
        }

        private async Task RunOperation(string title, string[] arguments, string stdin)
        {
            if (busy) return;
            busy = true;
            AppendStatus(title + "\r\n请稍候……");
            try
            {
                var result = await Task.Run(() => CallCore(arguments, stdin, arguments.Contains("start-client") ? 60 : 25));
                if (result.Response != null) ApplyResponse(result.Response, true);
                if (result.Response == null || !result.Response.ok)
                {
                    ShowFailure(result.Error ?? (result.Response == null ? "启动器组件没有返回结果。" : result.Response.message));
                }
            }
            finally
            {
                busy = false;
            }
            await RefreshStatus(false);
        }

        private async Task RefreshStatus(bool initialize)
        {
            if (busy || statusPending || !File.Exists(corePath)) return;
            statusPending = true;
            try
            {
                var result = await Task.Run(() => CallCore(new[] { "-action", "status" }, null, 8));
                if (result.Response != null && result.Response.ok)
                {
                    ApplyResponse(result.Response, false);
                    if (initialize && result.Response.status != null && result.Response.status.settings != null) ApplySettings(result.Response.status.settings);
                }
                else if (initialize)
                {
                    ShowFailure(result.Error ?? "无法读取启动器状态。");
                }
            }
            finally { statusPending = false; }
        }

        private void ApplySettings(NetworkSettings settings)
        {
            if (settings == null) return;
            mode.SelectedIndex = settings.mode == "lan-host" ? 1 : settings.mode == "remote-host" ? 2 : 0;
            serverIP.Text = settings.server_ip ?? "127.0.0.1";
            clientIP.Text = settings.client_server_ip ?? settings.server_ip ?? "127.0.0.1";
            gmRemote.Checked = settings.gm_remote && mode.SelectedIndex != 0;
            UpdateMode();
        }

        private void ApplyResponse(CoreResponse response, bool showMessage)
        {
            PopulateIPCandidates(response.ip_candidates);
            var s = response.status;
            if (s == null) return;
            serverState.Text = s.server_running ? "● 正在运行" : "● 未运行";
            serverState.ForeColor = s.server_running ? Success : Stopped;
            clientState.Text = "● " + s.client_count + " 个运行中";
            clientState.ForeColor = s.client_count > 0 ? Success : Stopped;
            var url = String.IsNullOrWhiteSpace(s.gm_url) ? "http://127.0.0.1:18100/gm/" : s.gm_url;
            gmURL.Text = (url.Contains("127.0.0.1") ? "本机地址：" : "GM 地址：") + url + (url.Contains("127.0.0.1") ? "（本机访问无需密码）" : "");
            gmURL.Tag = url;
            var lines = new List<string>();
            lines.Add(s.server_running ? "服务端：正在运行（PID " + s.server_pid + "）" : "服务端：未运行");
            if (s.settings != null)
            {
                var bind = s.settings.mode == "local" ? "127.0.0.1" : "0.0.0.0";
                lines.Add("监听地址：" + bind + "；客户端填写：" + s.settings.server_ip);
            }
            lines.Add("客户端：" + s.client_count + " 个运行中");
            if (showMessage && !String.IsNullOrWhiteSpace(response.message)) lines.Insert(0, response.message);
            status.Text = String.Join(Environment.NewLine, lines);
        }

        private CoreCallResult CallCore(string[] arguments, string stdin, int timeoutSeconds)
        {
            try
            {
                var info = new ProcessStartInfo
                {
                    FileName = corePath,
                    WorkingDirectory = root,
                    UseShellExecute = false,
                    CreateNoWindow = true,
                    RedirectStandardOutput = true,
                    RedirectStandardError = true,
                    StandardOutputEncoding = Encoding.UTF8,
                    StandardErrorEncoding = Encoding.UTF8,
                    RedirectStandardInput = stdin != null,
                    Arguments = JoinArguments(new[] { "-root", root }.Concat(arguments))
                };
                using (var process = Process.Start(info))
                {
                    if (process == null) return new CoreCallResult { Error = "无法启动本地控制器。" };
                    if (stdin != null)
                    {
                        process.StandardInput.Write(stdin);
                        process.StandardInput.Close();
                    }
                    var stdoutTask = process.StandardOutput.ReadToEndAsync();
                    var stderrTask = process.StandardError.ReadToEndAsync();
                    if (!process.WaitForExit(timeoutSeconds * 1000))
                    {
                        try { process.Kill(); } catch { }
                        return new CoreCallResult { Error = "操作超时，已停止本次控制器。服务端和客户端状态会在下一次刷新时重新识别。" };
                    }
                    Task.WaitAll(stdoutTask, stderrTask);
                    var stdout = stdoutTask.Result.Trim();
                    var stderr = stderrTask.Result.Trim();
                    CoreResponse response = null;
                    if (stdout.Length > 0)
                    {
                        try { response = serializer.Deserialize<CoreResponse>(stdout); }
                        catch (Exception ex) { return new CoreCallResult { Error = "控制器返回格式错误：" + ex.Message + Environment.NewLine + stdout }; }
                    }
                    return new CoreCallResult { Response = response, Error = response != null && !response.ok ? response.message : (stderr.Length > 0 ? stderr : null) };
                }
            }
            catch (Exception ex) { return new CoreCallResult { Error = ex.Message }; }
        }

        private static string JoinArguments(IEnumerable<string> values)
        {
            return String.Join(" ", values.Select(QuoteArgument));
        }

        private static string QuoteArgument(string value)
        {
            if (value == null) return "\"\"";
            return "\"" + value.Replace("\\", "\\\\").Replace("\"", "\\\"") + "\"";
        }

        private void AppendStatus(string message)
        {
            status.Text = message;
        }

        private void ShowFailure(string message)
        {
            status.Text = "失败：" + message + Environment.NewLine + "诊断日志：" + Path.Combine(root, "runtime", "logs", "launcher-ui.log");
            MessageBox.Show(this, message + Environment.NewLine + Environment.NewLine + "诊断日志：" + Path.Combine(root, "runtime", "logs", "launcher-ui.log"), "QQ堂启动器", MessageBoxButtons.OK, MessageBoxIcon.Error);
        }
    }
}
