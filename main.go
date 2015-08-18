package main

import (
  "net/http"
  "fmt"
  "sync"
  "os"
  "syscall"
  "log"
  "path/filepath"
  "time"
  "strings"
  "os/exec"
  "io/ioutil"
  "encoding/json"
  "github.com/levigross/grequests"
  id3       "github.com/mikkyang/id3-go"
  transport "google.golang.org/api/googleapi/transport"
  youtube   "google.golang.org/api/youtube/v3"
)

var _ = filepath.Join
var _ = grequests.Get
var _ = fmt.Println
var _ = time.Sleep
var _ = strings.Replace

const (
  playlist = "PL1531805E486A97FF" // the REAL Italo
  //playlist = "PLQh1lAYHwN7h0GJydjLRPrqghcg-_t6x_" // actually short Italo
  //playlist = "RDmbJ0aXxpTfM" // nightcore, not Italo
  maxListItems = 50
  youtubeDl = "youtube-dl"
  ffmpeg = "ffmpeg"
)

var (
  dirname       string = "outfiles"
  googleAPIKey  string
  shouldConvert bool   = true
  Artist        string = "Italo"
  Album         string = "Italo Disco Heaven"
)

func init() {
  // Check for command-line dependencies
  for _,dependency := range []string{youtubeDl, ffmpeg} {
    if _, err := exec.LookPath(dependency); err != nil {
      log.Fatalf("Must have %s in your PATH", dependency)
    }
  }

  // Make output directory
  if err := os.Mkdir(dirname, 0777); err != nil && !os.IsExist(err) {
    log.Fatal("Could not make directory", dirname, "for output files")
  }

  // @hack - Increase file descriptor limt
  const PLAYLIST_SIZE_LIMIT, SUBPROCS_PER_VIDEO = 200, 2
  // max size of YouTubePlaylist, subprocesses needed to download and convert video
  fdLimit := new(syscall.Rlimit)
  err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, fdLimit)
  fdLimit.Cur += PLAYLIST_SIZE_LIMIT * SUBPROCS_PER_VIDEO
  err  = syscall.Setrlimit(syscall.RLIMIT_NOFILE, fdLimit)
  if err != nil {
    log.Fatal(err.Error())
  }

  // Unpack configuration
  type config struct {
    GoogleAPIKey string `json:"google_api_key"`
  }
  configJson, err := ioutil.ReadFile("config.json")
  if err != nil {
    // config.json missing
    log.Fatal("Must have config.json file to run")
  }
  conf := new(config)
  err = json.Unmarshal(configJson, conf)
  if err != nil {
    // unmarshal error
    log.Fatal("config.json is formatted incorrectly")
  }
  if googleAPIKey = conf.GoogleAPIKey; googleAPIKey == "" {
    // google_api_key missing from config
    log.Fatal("Google API Key is missing from config.json")
  }
}

func main() {
  // Start up the YouTube service
  service, err := youtube.New(&http.Client{
    Transport: &transport.APIKey{Key: googleAPIKey},
  })
  if err != nil {
    log.Fatal(err.Error())
  }
  
  playlistItems := make([]*OrderedPlaylistItem, 0)
  sieve := make(chan *youtube.PlaylistItem)
  
  // fetch the video ids
  go playlistItemsSieve(service, playlist, sieve)
  
  // dispatch the downloads
  var counter int = 1
  for video := range sieve {
    orderedVideo := OrderedPlaylistItem{video, counter, 1}
    playlistItems = append(playlistItems, &orderedVideo)
    counter++
  }

  wg := new(sync.WaitGroup)
  for _, video := range playlistItems {
    wg.Add(1)
    go func(v *OrderedPlaylistItem) {
      var e error = v.Download()
      if shouldConvert && e == nil {
        e = v.ConvertToMp3(Artist, Album)
        os.Remove(v.M4aFname())
      }
      if e != nil {
        fmt.Println(e.Error())
      }
      wg.Done()
    }(video)
  }
  wg.Wait()
  
}

func (video *OrderedPlaylistItem) M4aFname() string {
  return filepath.Join(dirname, fmt.Sprintf("%d - %s.m4a", video.PositionInPlaylist, video.Snippet.Title))
}

func (video *OrderedPlaylistItem) Mp3Fname() string {
  return strings.TrimSuffix(video.M4aFname(), "m4a") + "mp3"
}

func (video *OrderedPlaylistItem) ConvertToMp3(artist, album string) error {
  if _, err := os.Stat(video.M4aFname()); os.IsNotExist(err) {
    return err
  }
  cmd := exec.Command(ffmpeg, "-i", video.M4aFname(), "-acodec", "libmp3lame", "-ab", "128k", video.Mp3Fname())
  _, err := cmd.Output()
  
  mp3File, err := id3.Open(video.Mp3Fname())
  defer mp3File.Close()
  if err == nil {
    mp3File.SetArtist(artist)
    mp3File.SetAlbum(album)
  }

  return err
}

func (video *OrderedPlaylistItem) Download() error {
  if video.RetriesLeft < 1 {
    // look for the recursive base case, exit if max retries exceeded
    return fmt.Errorf("Exceeded maximum retries for video %s", video.ContentDetails.VideoId)
  }
  cmd := exec.Command(youtubeDl, "-o", video.M4aFname(), "https://youtube.com/watch?v="+video.ContentDetails.VideoId, "-f", "141/140")
  output, err := cmd.Output()
  fmt.Println(string(output))



  if err == nil {
    return nil
  } else {
    video.RetriesLeft -= 1
    video.Download()
    return err
  }
}

func playlistItemsSieve(service *youtube.Service, playlistId string, output chan *youtube.PlaylistItem) {
  var nextPageToken string
  for {
    req := service.PlaylistItems.List("snippet,contentDetails").PlaylistId(playlistId).MaxResults(maxListItems)
    if nextPageToken != "" {
      // we are paginating
      req = req.PageToken(nextPageToken)
    }
    playlist, err := req.Do()
    if err != nil {
      panic(err)
    }

    for _, video := range playlist.Items {
      output <- video
    }

    nextPageToken = playlist.NextPageToken
    if nextPageToken == "" {
      break
    }
  }
  close(output)
}

type OrderedPlaylistItem struct {
  *youtube.PlaylistItem
  PositionInPlaylist    int
  RetriesLeft           int
}
