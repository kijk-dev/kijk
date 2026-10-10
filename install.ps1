# Installs or updates kijk on Windows:
#   irm https://github.com/kijk-dev/kijk/releases/latest/download/install.ps1 | iex
# KIJK_INSTALL_DIR picks the folder. It needs no admin rights.

function Install-Kijk {
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue' # the progress bar makes Windows PowerShell 5.1 downloads crawl
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

    $releases = if ($env:KIJK_RELEASES_URL) { $env:KIJK_RELEASES_URL.TrimEnd('/') } else { 'https://github.com/kijk-dev/kijk/releases' }
    # 32-bit PowerShell on a 64-bit Windows reports x86 here and the real CPU in PROCESSOR_ARCHITEW6432.
    $cpu = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
    $arch = switch ($cpu) {
        'AMD64' { 'amd64' }
        'ARM64' { 'arm64' }
        default { throw "kijk has no binary for Windows on $cpu" }
    }
    $name = "kijk-windows-$arch.exe"
    $dir = if ($env:KIJK_INSTALL_DIR) { $env:KIJK_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\kijk' }
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    $exe = Join-Path $dir 'kijk.exe'
    $tmp = Join-Path $dir ".kijk-install-$PID.exe"

    try {
        Write-Host "Downloading $name"
        $sums = (Invoke-WebRequest -UseBasicParsing "$releases/latest/download/checksums.txt").Content
        if ($sums -is [byte[]]) { $sums = [Text.Encoding]::UTF8.GetString($sums) }
        Invoke-WebRequest -UseBasicParsing "$releases/latest/download/$name" -OutFile $tmp
        $want = @($sums -split "`n" | ForEach-Object {
            $f = $_.Trim() -split '\s+'
            if ($f.Count -eq 2 -and $f[1].TrimStart('*') -eq $name) { $f[0].ToLower() }
        })
        if ($want.Count -ne 1) { throw "checksums.txt has no single line for $name. Nothing was installed." }
        # Not Get-FileHash: Windows PowerShell started from PowerShell 7 cannot load it.
        $sha = [Security.Cryptography.SHA256]::Create()
        $file = [IO.File]::OpenRead($tmp)
        try { $hash = -join ($sha.ComputeHash($file) | ForEach-Object { $_.ToString('x2') }) }
        finally { $file.Dispose(); $sha.Dispose() }
        if ($hash -ne $want[0]) {
            throw "$name does not match checksums.txt. Nothing was installed."
        }
        $new = ((& $tmp version) -replace '^kijk ', '').Trim()
        if (-not $new) { throw 'the downloaded kijk does not run on this machine' }

        $old = ''
        if (Test-Path $exe) {
            $old = ((& $exe -version 2>$null) -replace '^kijk ', '').Trim()
            # Windows will not overwrite a running kijk.exe, but lets it be renamed.
            Remove-Item "$exe.old" -Force -ErrorAction SilentlyContinue
            Rename-Item $exe 'kijk.exe.old'
        }
        try {
            Move-Item $tmp $exe
        } catch {
            if (Test-Path "$exe.old") { Rename-Item "$exe.old" 'kijk.exe' }
            throw
        }
        Remove-Item "$exe.old" -Force -ErrorAction SilentlyContinue
    } finally {
        Remove-Item $tmp -Force -ErrorAction SilentlyContinue
    }

    if ($old -and $old -ne $new) { Write-Host "kijk $old -> $new in $dir" } else { Write-Host "kijk $new is installed in $dir" }
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (-not (($userPath -split ';') -contains $dir)) {
        [Environment]::SetEnvironmentVariable('Path', (@($userPath, $dir) | Where-Object { $_ }) -join ';', 'User')
        $env:Path = "$env:Path;$dir"
        Write-Host "Added $dir to your PATH. New terminals will find kijk."
    } else {
        Write-Host 'Run kijk to start it.'
    }
}

try {
    Install-Kijk
} catch {
    Write-Host "kijk install: $($_.Exception.Message)" -ForegroundColor Red
    # Under irm | iex, exit would close the user's PowerShell window; only a script file exits.
    if ($PSCommandPath) { exit 1 }
}
