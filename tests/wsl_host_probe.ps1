param([Parameter(Mandatory=$true)][string]$Payload)
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
$configuration = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($Payload)) | ConvertFrom-Json
$wslExecutable = Join-Path ([Environment]::GetFolderPath('Windows')) 'System32\wsl.exe'
$records = @()
foreach ($case in $configuration.cases) {
    $start = New-Object System.Diagnostics.ProcessStartInfo
    $start.FileName = $wslExecutable
    # Prepared with Python's Windows argv encoder; no cmd.exe or shell expansion.
    $start.Arguments = $case.windows_arguments
    $start.UseShellExecute = $false
    $start.RedirectStandardOutput = $true
    $start.RedirectStandardError = $true
    $process = New-Object System.Diagnostics.Process
    $process.StartInfo = $start
    $outputBytes = New-Object System.IO.MemoryStream
    $errorBytes = New-Object System.IO.MemoryStream
    try {
        [void]$process.Start()
        # WSL-native errors can be UTF-16LE while Linux output is UTF-8.
        # Preserve bytes until the receiver can distinguish those formats.
        $stdout = $process.StandardOutput.BaseStream.CopyToAsync($outputBytes)
        $stderr = $process.StandardError.BaseStream.CopyToAsync($errorBytes)
        $finished = $process.WaitForExit(20000)
        if (-not $finished) {
            # This handle belongs to this invocation, never a WSL distribution.
            $process.Kill()
            [void]$process.WaitForExit(5000)
        }
        $outReady = $stdout.Wait(2000)
        $errReady = $stderr.Wait(2000)
        $records += [ordered]@{
            name = $case.name
            completed = $finished
            exit_code = $(if ($process.HasExited) { $process.ExitCode } else { $null })
            stdout_base64 = $(if ($outReady) { [Convert]::ToBase64String($outputBytes.ToArray()) } else { '' })
            stderr_base64 = $(if ($errReady) { [Convert]::ToBase64String($errorBytes.ToArray()) } else { '' })
            output_complete = ($outReady -and $errReady)
        }
    } finally {
        $process.Dispose()
        $outputBytes.Dispose()
        $errorBytes.Dispose()
    }
}
[ordered]@{
    platform = [Environment]::OSVersion.Platform.ToString()
    powershell_version = $PSVersionTable.PSVersion.ToString()
    windows_pid = $PID
    cases = $records
} | ConvertTo-Json -Depth 8 -Compress
