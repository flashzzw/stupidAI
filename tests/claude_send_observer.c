#define _GNU_SOURCE
#include <dlfcn.h>
#include <sys/socket.h>
#include <sys/syscall.h>
#include <sys/stat.h>
#include <unistd.h>
#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <errno.h>
#include <string.h>

/* Passive instrumentation: call the original libc function unchanged, record
 * only metadata and the presence of the fixed initialize method, preserve errno.
 * Never retry, replace a syscall, or alter the result of a denied operation. */
static void note(const char *name,int fd,const void *buf,size_t len,ssize_t result,int error){
  const char *path=getenv("NETCAP_SEND_OBSERVER_LOG");
  if(!path)return;
  int out=open(path,O_WRONLY|O_APPEND|O_CREAT|O_CLOEXEC,0600);
  if(out<0)return;
  struct stat s;int is_socket=fstat(fd,&s)==0 && S_ISSOCK(s.st_mode);
  int init=buf && memmem(buf,len,"\"method\":\"initialize\"",21)!=NULL;
  char row[240];int n=snprintf(row,sizeof(row),"{\"pid\":%d,\"call\":\"%s\",\"fd\":%d,\"socket\":%d,\"bytes\":%zu,\"initialize\":%d,\"result\":%zd,\"errno\":%d}\n",getpid(),name,fd,is_socket,len,init,result,error);
  if(n>0 && n<(int)sizeof(row))syscall(SYS_write,out,row,n);
  close(out);
}
ssize_t send(int fd,const void *buf,size_t len,int flags){
  int incoming_errno=errno;
  ssize_t (*original)(int,const void*,size_t,int)=dlsym(RTLD_NEXT,"send");
  errno=incoming_errno;
  ssize_t result=original(fd,buf,len,flags);int error=errno;
  note("send",fd,buf,len,result,result<0?error:0);errno=error;return result;
}
ssize_t sendto(int fd,const void *buf,size_t len,int flags,const struct sockaddr *addr,socklen_t addrlen){
  int incoming_errno=errno;
  ssize_t (*original)(int,const void*,size_t,int,const struct sockaddr*,socklen_t)=dlsym(RTLD_NEXT,"sendto");
  errno=incoming_errno;
  ssize_t result=original(fd,buf,len,flags,addr,addrlen);int error=errno;
  note("sendto",fd,buf,len,result,result<0?error:0);errno=error;return result;
}
