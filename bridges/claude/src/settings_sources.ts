import { constants, openSync, closeSync, fstatSync, statSync, readSync, realpathSync, lstatSync } from 'node:fs';
import { isAbsolute, dirname, basename, join } from 'node:path';
import { createHash } from 'node:crypto';
import { NotSent, record } from './protocol.js';

export type SettingsSource = {path:string;resolved_path:string;kind:'file'|'absent';sha256?:string;mode?:number};

// Native hook listings identify these two local settings groups, not file
// paths. Every file in the group remains separately bound and rechecked.
export function adoptedSettingsHookSources(previous: SettingsSource[] | undefined, target: SettingsSource[] | undefined): string[] {
  if (!target) return [];
  if (!previous || previous.length !== target.length || previous.some((s,i)=>s.path!==target[i]?.path)) throw new NotSent('local settings adoption source set mismatch');
  const sources=new Set<string>();
  for(let i=0;i<target.length;i++){
    const a=previous[i]!,b=target[i]!;
    if(a.kind===b.kind&&a.resolved_path===b.resolved_path&&a.sha256===b.sha256&&a.mode===b.mode)continue;
    if(basename(b.path)==='settings.json')sources.add('projectSettings');
    if(basename(b.path)==='settings.local.json')sources.add('localSettings');
  }
  return [...sources];
}

export function settingsSources(value: unknown): SettingsSource[] {
  if (!Array.isArray(value) || value.length < 2 || value.length > 3) throw new NotSent('invalid local settings sources');
  const paths=new Set<string>();
  return value.map(item=>{
    const s=record(item);
    if(typeof s.path!=='string'||typeof s.resolved_path!=='string'||!isAbsolute(s.path)||!isAbsolute(s.resolved_path)||s.path.includes('\0')||s.resolved_path.includes('\0')||s.path.length>16_384||s.resolved_path.length>16_384||paths.has(s.path))throw new NotSent('invalid local settings path');
    paths.add(s.path);
    if(s.kind==='absent' && s.sha256===undefined && s.mode===undefined)return {path:s.path,resolved_path:s.resolved_path,kind:'absent'};
    const mode=s.mode===undefined?0:s.mode;
    if(s.kind!=='file'||typeof s.sha256!=='string'||! /^[a-f0-9]{64}$/.test(s.sha256)||!Number.isInteger(mode)||Number(mode)<0||Number(mode)>0xffffffff)throw new NotSent('invalid local settings file version');
    return {path:s.path,resolved_path:s.resolved_path,kind:'file',sha256:s.sha256,mode:Number(mode)};
  });
}

function absentPath(path:string): string {
  try { lstatSync(path); } catch(e) {
    if((e as NodeJS.ErrnoException).code!=='ENOENT')throw e;
    let parent=dirname(path),suffix=basename(path);
    for(let i=0;i<64;i++){
      try {const resolved=realpathSync(parent);if(!statSync(resolved).isDirectory())break;return join(resolved,suffix);}
      catch(error){
        try {lstatSync(parent);break;}catch(e){if((e as NodeJS.ErrnoException).code!=='ENOENT'||parent===dirname(parent))throw error;}
        suffix=join(basename(parent),suffix);parent=dirname(parent);
      }
    }
  }
  throw new NotSent('local settings absence cannot be verified');
}

function goMode(mode:number) {
  return (mode&0o777) | (mode&0o4000?1<<23:0) | (mode&0o2000?1<<22:0) | (mode&0o1000?1<<20:0);
}

// Sample only caller-bound local files. This is not a replacement for Go's
// Git/cwd source-set discovery and does not claim an atomic native file read.
export function verifySettingsSources(sources: SettingsSource[] | undefined) {
  for(const source of sources??[]){
    let fd:number|undefined;
    try {
      if(source.kind==='absent') {if(absentPath(source.path)!==source.resolved_path)throw Error();continue;}
      const resolved=realpathSync(source.path);
      if(resolved!==source.resolved_path)throw Error();
      fd=openSync(source.path,constants.O_RDONLY|constants.O_NONBLOCK|constants.O_NOCTTY);
      const before=fstatSync(fd);
      if(!before.isFile()||before.size>2*1024*1024||goMode(before.mode)!==source.mode)throw Error();
      // File descriptors are only read after bounded regular-file validation.
      const buffer=Buffer.alloc(before.size+1);let length=0,read=0;
      while(length<buffer.length&&(read=readSync(fd,buffer,length,buffer.length-length,null))>0)length+=read;
      const body=buffer.subarray(0,length),after=fstatSync(fd),current=statSync(source.path);
      if(body.length!==before.size||after.size!==before.size||current.size!==before.size||after.mtimeMs!==before.mtimeMs||current.mtimeMs!==before.mtimeMs||after.mode!==before.mode||current.mode!==before.mode||current.dev!==before.dev||current.ino!==before.ino||realpathSync(source.path)!==resolved||createHash('sha256').update(body).digest('hex')!==source.sha256)throw Error();
    }catch{throw new NotSent('Claude 本地设置文件版本无法核对：'+source.path);}
    finally{if(fd!==undefined)closeSync(fd);}
  }
}
