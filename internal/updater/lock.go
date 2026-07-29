package updater

import (
 "errors"
 "fmt"
 "os"
 "path/filepath"
 "strconv"
 "time"
)

var ErrLocked = errors.New("another update is already running")
type Lock struct{path string}

func AcquireLock(location string, stale time.Duration) (*Lock,error) {
 if stale<=0 { stale=30*time.Minute }
 p:=location
 dir:=location
 if filepath.Ext(location)==".lock" { dir=filepath.Dir(location) } else { p=filepath.Join(location,"update.lock") }
 if err:=os.MkdirAll(dir,0700);err!=nil{return nil,err}
 for a:=0;a<2;a++ {
  f,e:=os.OpenFile(p,os.O_CREATE|os.O_EXCL|os.O_WRONLY,0600)
  if e==nil { _,_=fmt.Fprintf(f,"%d\n%s\n",os.Getpid(),time.Now().UTC().Format(time.RFC3339));_=f.Close();return &Lock{p},nil }
  if !os.IsExist(e){return nil,e}
  st,se:=os.Stat(p);if se==nil&&time.Since(st.ModTime())>stale{_=os.Remove(p);continue}
  b,_:=os.ReadFile(p);return nil,fmt.Errorf("%w (lock %s, pid %s)",ErrLocked,p,firstLine(string(b)))
 }
 return nil,fmt.Errorf("%w",ErrLocked)
}
func(l *Lock)Release()error{if l==nil||l.path==""{return nil};return os.Remove(l.path)}
func firstLine(s string)string{for i,r:=range s{if r=='\n'{return s[:i]}};if _,e:=strconv.Atoi(s);e==nil{return s};return "unknown"}
