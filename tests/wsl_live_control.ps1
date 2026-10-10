# Test-only Windows byte transport. Every child is an owned wsl.exe handle.
$ErrorActionPreference = 'Stop'
[Console]::InputEncoding = New-Object System.Text.UTF8Encoding($false)
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
$children = @{}
function Reply($value) {
    [Console]::Out.WriteLine(($value | ConvertTo-Json -Depth 12 -Compress))
    [Console]::Out.Flush()
}
function Stop-Child($child, $kill) {
    if (-not $child.process.HasExited) {
        if ($kill) { $child.process.Kill() }
        else { $child.process.StandardInput.Close() }
        if (-not $child.process.WaitForExit(5000)) {
            $child.process.Kill()
            [void]$child.process.WaitForExit(5000)
        }
    }
    $errorReady = $child.errorTask.Wait(2000)
    return @{
        pid = $child.pid
        exited = $child.process.HasExited
        exit_code = $(if ($child.process.HasExited) { $child.process.ExitCode } else { $null })
        stderr_complete = $errorReady
        stderr_base64 = $(if ($errorReady) { [Convert]::ToBase64String($child.errorBytes.ToArray()) } else { '' })
    }
}
Reply @{ready=$true; platform=[Environment]::OSVersion.Platform.ToString(); windows_pid=$PID;
    powershell_version=$PSVersionTable.PSVersion.ToString()}
try {
    while ($null -ne ($line = [Console]::In.ReadLine())) {
        try {
            $request = $line | ConvertFrom-Json
            $result = $null
            switch ($request.op) {
                'start' {
                    if ($children.ContainsKey($request.name)) { throw 'Duplicate child name' }
                    $start = New-Object System.Diagnostics.ProcessStartInfo
                    $start.FileName = Join-Path ([Environment]::GetFolderPath('Windows')) 'System32\wsl.exe'
                    $start.Arguments = $request.arguments
                    $start.UseShellExecute = $false
                    $start.RedirectStandardInput = $true
                    $start.RedirectStandardOutput = $true
                    $start.RedirectStandardError = $true
                    $process = New-Object System.Diagnostics.Process
                    $process.StartInfo = $start
                    [void]$process.Start()
                    $errors = New-Object System.IO.MemoryStream
                    $children[$request.name] = @{
                        process=$process; pid=$process.Id; errorBytes=$errors
                        errorTask=$process.StandardError.BaseStream.CopyToAsync($errors)
                        readTask=$null; buffer=(New-Object byte[] 65536)
                    }
                    $result = @{pid=$process.Id; executable=$start.FileName; arguments=$start.Arguments}
                }
                'write' {
                    $child = $children[$request.name]
                    $bytes = [Convert]::FromBase64String($request.data)
                    $child.process.StandardInput.BaseStream.Write($bytes, 0, $bytes.Length)
                    $child.process.StandardInput.BaseStream.Flush()
                    $result = @{written=$bytes.Length}
                }
                'read' {
                    $child = $children[$request.name]
                    if ($null -eq $child.readTask) {
                        $child.readTask = $child.process.StandardOutput.BaseStream.ReadAsync($child.buffer, 0, $child.buffer.Length)
                    }
                    if (-not $child.readTask.Wait(10000)) {
                        $result = @{timeout=$true}
                    } else {
                        $count = $child.readTask.Result
                        $child.readTask = $null
                        $result = @{eof=($count -eq 0); data=[Convert]::ToBase64String($child.buffer, 0, $count)}
                    }
                }
                'stop' { $result = Stop-Child $children[$request.name] $request.kill }
                default { throw 'Unknown transport operation' }
            }
            Reply @{ok=$true; result=$result}
        } catch {
            Reply @{ok=$false; error=$_.Exception.Message}
        }
    }
} finally {
    foreach ($child in $children.Values) {
        [void](Stop-Child $child $true)
        $child.process.Dispose()
        $child.errorBytes.Dispose()
    }
}
