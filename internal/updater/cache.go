package updater

import (
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "sort"
 "time"
 "github.com/o25160526-pip/go-selfupdate-template/internal/version"
)
type Cache struct { Dir string; TTL time.Duration; KeepBlobs int }
type CacheEntry struct { SHA256 string `json:"sha256"`; Path string `json:"path"`; Size int64 `json:"size"`; LastUsed time.Time `json:"last_used"`; Verified bool `json:"verified"` }
type BlobInfo struct { SHA256 string `json:"sha256"`; Path string `json:"path,omitempty"`; Size int64 `json:"size,omitempty"`; Version version.Version `json:"version"`; Asset string `json:"asset"`; Source string `json:"source"`; LastUsed time.Time `json:"last_used"` }
type cacheIndex struct { Entries map[string]CacheEntry `json:"entries"`; Blobs map[string]BlobInfo `json:"blobs,omitempty"` }
type releaseCache struct { Releases []Release `json:"releases"`; ETag string `json:"etag,omitempty"`; Fetched time.Time `json:"fetched"` }
func NewCache(dir string,ttl time.Duration,keep int)*Cache{return &Cache{Dir:dir,TTL:ttl,KeepBlobs:keep}}
func(c Cache)blobsDir()string{return filepath.Join(c.Dir,"blobs")};func(c Cache)BlobPath(s string)string{return filepath.Join(c.blobsDir(),s)};func(c Cache)PartialPath(s string)string{return c.BlobPath(s)+".part"};func(c Cache)metaDir()string{return filepath.Join(c.Dir,"meta")};func(c Cache)Ensure()error{return os.MkdirAll(c.blobsDir(),0700)}
func(c Cache)HasBlob(s string)bool{st,e:=os.Stat(c.BlobPath(s));return e==nil&&st.Mode().IsRegular()};func(c Cache)Touch(s string){i,_:=c.load();if x,ok:=i.Entries[s];ok{x.LastUsed=time.Now().UTC();i.Entries[s]=x;_=c.save(i)}}
func(c Cache)Blobs()([]BlobInfo,error){i,e:=c.load();if e!=nil{return nil,e};o:=make([]BlobInfo,0,len(i.Blobs));for _,b:=range i.Blobs{if c.HasBlob(b.SHA256){o=append(o,b)}};sort.Slice(o,func(i,j int)bool{return o[i].Version.After(o[j].Version)});return o,nil};func(c Cache)Clear()error{if e:=os.RemoveAll(c.Dir);e!=nil{return e};return c.Ensure()}
func(c Cache)Lookup(s string)(string,bool){if !c.HasBlob(s){return "",false};i,_:=c.load();e:=i.Entries[s];e.SHA256,e.Path,e.Verified,e.LastUsed=s,c.BlobPath(s),true,time.Now().UTC();i.Entries[s]=e;_=c.save(i);return e.Path,true}
func(c Cache)SaveBlobInfo(b BlobInfo)error{i,_:=c.load();if b.Path==""{b.Path=c.BlobPath(b.SHA256)};if st,e:=os.Stat(b.Path);e==nil{b.Size=b.Size;if b.Size==0{b.Size=st.Size()}};b.LastUsed=time.Now().UTC();i.Blobs[b.SHA256]=b;e:=i.Entries[b.SHA256];e.SHA256,e.Path,e.Size,e.Verified,e.LastUsed=b.SHA256,b.Path,b.Size,true,b.LastUsed;i.Entries[b.SHA256]=e;return c.save(i)}
func(c Cache)Commit(p,s string)(string,error){if e:=c.Ensure();e!=nil{return "",e};a,e:=SHA256File(p);if e!=nil{return "",e};if a!=s{return "",fmt.Errorf("download checksum mismatch: got %s want %s",a,s)};f:=c.BlobPath(s);if e=os.Rename(p,f);e!=nil{return "",e};st,e:=os.Stat(f);if e!=nil{return "",e};i,_:=c.load();i.Entries[s]=CacheEntry{SHA256:s,Path:f,Size:st.Size(),LastUsed:time.Now().UTC(),Verified:true};if e=c.save(i);e!=nil{return "",e};_,_=c.Prune();return f,nil}
func(c Cache)Prune(x ...int)(int,error){k:=c.KeepBlobs;if len(x)>0&&x[0]>0{k=x[0]};if k<=0{k=6};i,e:=c.load();if e!=nil{return 0,e};a:=make([]CacheEntry,0,len(i.Entries));for _,v:=range i.Entries{a=append(a,v)};sort.Slice(a,func(i,j int)bool{return a[i].LastUsed.After(a[j].LastUsed)});if len(a)<=k{return 0,c.save(i)};n:=0;for _,v:=range a[k:]{if os.Remove(v.Path)==nil{n++};delete(i.Entries,v.SHA256);delete(i.Blobs,v.SHA256)};return n,c.save(i)}
func(c Cache)LoadReleases(s,ch string)([]Release,string,bool){b,e:=os.ReadFile(filepath.Join(c.metaDir(),s+"-"+ch+".json"));if e!=nil{return nil,"",false};var v releaseCache;if json.Unmarshal(b,&v)!=nil{return nil,"",false};return v.Releases,v.ETag,c.TTL<=0||time.Since(v.Fetched)<=c.TTL};func(c Cache)SaveReleases(s,ch string,r []Release,e string)error{if e:=os.MkdirAll(c.metaDir(),0700);e!=nil{return e};b,e:=json.MarshalIndent(releaseCache{r,e,time.Now().UTC()},"","  ");if e!=nil{return e};return os.WriteFile(filepath.Join(c.metaDir(),s+"-"+ch+".json"),append(b,'\n'),0600)};func(c Cache)TouchMeta(s,ch string){r,e,_:=c.LoadReleases(s,ch);_=c.SaveReleases(s,ch,r,e)}
func(c Cache)load()(cacheIndex,error){i:=cacheIndex{Entries:map[string]CacheEntry{},Blobs:map[string]BlobInfo{}};b,e:=os.ReadFile(filepath.Join(c.Dir,"index.json"));if os.IsNotExist(e){return i,nil};if e!=nil{return i,e};if e=json.Unmarshal(b,&i);e!=nil{return i,e};if i.Entries==nil{i.Entries=map[string]CacheEntry{}};if i.Blobs==nil{i.Blobs=map[string]BlobInfo{}};return i,nil};func(c Cache)save(i cacheIndex)error{if e:=c.Ensure();e!=nil{return e};b,e:=json.MarshalIndent(i,"","  ");if e!=nil{return e};p:=filepath.Join(c.Dir,"index.json");t:=p+".tmp";if e=os.WriteFile(t,append(b,'\n'),0600);e!=nil{return e};return os.Rename(t,p)}
