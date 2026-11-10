"""Package only Git-visible source files for isolated Linux image builds."""
from pathlib import Path
import subprocess
import tarfile
root=Path(__file__).resolve().parents[1];result=subprocess.run(['git','ls-files','-c','-o','--exclude-standard','-z'],cwd=root,capture_output=True,check=True);files=sorted(set(p.decode('utf-8') for p in result.stdout.split(b'\x00') if p))
target=root/'.cache/extensions-source.tar';target.parent.mkdir(exist_ok=True)
with tarfile.open(target,'w') as archive:
    for name in files:
        path=root/name
        assert path.resolve().is_relative_to(root) and not path.is_symlink()
        if path.is_file():archive.add(path,arcname=name,recursive=False)
print('Public source context prepared; ignored env/cache/data excluded')
