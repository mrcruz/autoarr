package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

var downloadClientUrlTorrent string
var downloadClientUrl string
var rootDownloadPath string
var config Config

func main() {
	jsonFile, err := os.Open("/config/input.json")
	if err != nil {
		panic(err)
	}
	defer jsonFile.Close()

	byteValue, _ := ioutil.ReadAll(jsonFile)

	json.Unmarshal([]byte(byteValue), &config)

	// get variables
	maxPoolSize := config.PoolSize * 1000000 * 1000

	downloadClientUrl = config.DownloadClientUrl + "/api/v2/"
	downloadClientUrlTorrent = downloadClientUrl + "torrents/"

	// sanity check on env variables
	if config.RcloneRemote != "" {
		rcloneConfigPath := "/home/user/.config/rclone/rclone.conf"
		if _, err := os.Stat(rcloneConfigPath); os.IsNotExist(err) {
			panic("no rclone config found on " + rcloneConfigPath)
		}
	}

	if maxPoolSize <= 0 {
		panic("Pool Size not defined")
	}

	// get data from download client
	rootDownloadPath = GetRootDownloadPath()
	allDownloads := GetDownloadList()

	// sort them by priority
	sort.Stable(allDownloads)

	// get list of soon to be active and soon to be idle downloads, respecting pool size
	var activePool DownloadCollection
	var idlePool DownloadCollection

	var activeDownloadPoolSize float64
	for _, download := range allDownloads.Downloads {

		newDownloadSize := download.Size

		if download.IsIgnored() {
			if config.ConsiderIgnoredInPoolSize {
				activeDownloadPoolSize += newDownloadSize
			}

			continue
		}

		// enable torrent if fits on active pool, disable if it does not
		if activeDownloadPoolSize+newDownloadSize < maxPoolSize {
			activePool.Add(download)
			activeDownloadPoolSize += newDownloadSize
		} else {
			idlePool.Add(download)
		}
	}

	for _, download := range idlePool.Downloads {
		if download.ShouldBeRemoved() {
			download.Remove()
			continue
		}

		if download.Active == false {
			continue
		}

		Log("Stoping download: " + download.Name)

		download.Pause()

		if config.UseStash {
			download.Stash()
		}
	}

	for _, download := range activePool.Downloads {
		if config.RemoveOnlyWhenPoolIsFull == false && download.ShouldBeRemoved() {
			download.Remove()
			continue
		}

		if download.Active == true {
			continue
		}

		Log("Starting download: " + download.Name)

		if config.UseStash {
			download.Retrieve()
		}

		if config.RecheckOnResume {
			download.Recheck()
		}

		download.Resume()
		download.Reannounce()
	}
}

func (download Download) ShouldBeRemoved() bool {
	removeDownload := false
	for _, condition := range config.RemoveConditions {

		if condition.Value == 0 {
			panic("Value not defined for remove condition with field: " + condition.Field)
		}

		satisfiesCondition := false

		if condition.Invert {
			satisfiesCondition = download.GetFloat(condition.Field) < condition.Value
		} else {
			satisfiesCondition = download.GetFloat(condition.Field) > condition.Value
		}

		if satisfiesCondition {
			removeDownload = true
			if config.RemoveConditionInclusive == true {
				break
			}
		} else {
			removeDownload = false
			if config.RemoveConditionInclusive == false {
				break
			}
		}
	}
	return removeDownload
}

func (download Download) IsIgnored() bool {
	values := []string{download.Name, download.Tag, download.Category}
	blockItems := []string{config.IgnoreByName, config.IgnoreByTag, config.IgnoreByCategory}
	allowItems := []string{config.AllowByName, config.AllowByTag, config.AllowByCategory}

	for i, _ := range blockItems {
		if blockItems[i] == "" {
			continue
		}

		if strings.Contains(values[i], blockItems[i]) {
			return true
		}

	}

	for i, _ := range allowItems {
		if allowItems[i] == "" {
			continue
		}

		if !strings.Contains(values[i], allowItems[i]) {
			return true
		}
	}

	return false
}

func (download Download) GetRelativePath() string {
	return strings.TrimLeft(strings.ReplaceAll(download.ContentPath, rootDownloadPath, ""), "/")
}

func (download Download) GetIdlePath() string {
	idlePath := "/idle/"
	if config.RcloneRemote != "" {
		idlePath = config.RcloneRemote + ":/"
	}
	return idlePath + download.GetRelativePath()
}

func (download Download) GetActivePath() string {
	return "/active/" + download.GetRelativePath()
}

func Log(line string) {
	line = time.Now().Local().Format("2006-01-02 15:04:05") + ": " + line

	fmt.Println(line)

	f, err := os.OpenFile("/config/autoarr.log",
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)

	if err != nil {
		panic(err)
	}

	defer f.Close()
	if _, err := f.WriteString("\n" + line); err != nil {
		panic(err)
	}
}

func Rclone(command ...string) {
	cmd := exec.Command("rclone", command...)
	fmt.Println(cmd)
	if config.DoNotChangeFiles {
		return
	}
	cmd.Output()
}

func (download Download) Retrieve() {
	Rclone("copy", download.GetIdlePath(), download.GetActivePath())
}

func (download Download) Stash() {
	action := "move"
	if config.DoNotDestroyFiles {
		action = "copy"
	}
	Rclone(action, download.GetActivePath(), download.GetIdlePath())
}

func (download Download) Remove() {
	Log("Removing download: " + download.Name)

	if config.DoNotDestroyFiles {
		fmt.Println("Not removing download " + download.Name + " because DoNotDestroyFiles is true")
		return
	}
	Rclone("purge", download.GetIdlePath())
	Rclone("purge", download.GetActivePath())
	MakeDownloadClientRequest("delete?hashes=" + download.Hash + "&deleteFiles=true")
}

func (dc DownloadCollection) Len() int {
	return len(dc.Downloads)
}

func (dc DownloadCollection) Less(i, j int) bool {
	if config.SortInvertOrder {
		return dc.Downloads[i].GetFloat(config.SortField) > dc.Downloads[j].GetFloat(config.SortField)
	} else {
		return dc.Downloads[i].GetFloat(config.SortField) < dc.Downloads[j].GetFloat(config.SortField)
	}
}

func (dc DownloadCollection) Swap(i, j int) {
	temp := dc.Downloads[i]
	dc.Downloads[i] = dc.Downloads[j]
	dc.Downloads[j] = temp
}

func (dc *DownloadCollection) Add(download Download) {
	dc.Downloads = append(dc.Downloads, download)
}

func (dc *DownloadCollection) Union(addedDc DownloadCollection) {
	dc.Downloads = append(dc.Downloads, addedDc.Downloads...)
}

func (download Download) Recheck() {
	MakeDownloadClientRequest("recheck?hashes=" + download.Hash)
}

func (download Download) Reannounce() {
	MakeDownloadClientRequest("reannounce?hashes=" + download.Hash)
}

func (download Download) Resume() {
	MakeDownloadClientRequest("resume?hashes=" + download.Hash)
}

func (download Download) Pause() {
	MakeDownloadClientRequest("pause?hashes=" + download.Hash)
}

func (d Download) GetFloat(field string) float64 {
	if d.Raw[field] == nil {
		panic("Problem when trying to get field: " + field)
	}
	return d.Raw[field].(float64)
}

func MakeDownloadClientRequest(request string) {
	if config.DoNotChangeDownloadClient {
		fmt.Println("Skiping get: " + downloadClientUrlTorrent + request)
		return
	}
	http.Get(downloadClientUrlTorrent + request)
}

func GetRootDownloadPath() string {
	torrentClientUrl := downloadClientUrl + "app/defaultSavePath"
	resp, err := http.Get(torrentClientUrl)

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Fatal(err)
	}

	return string(body)
}

func GetDownloadList() DownloadCollection {
	torrentClientUrl := downloadClientUrlTorrent + "info"
	resp, err := http.Get(torrentClientUrl)

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Fatal(err)
	}

	if string(body) == "Forbidden" {
		panic("Forbidden: unable to access your download client")
	}

	var clientResponse []map[string]interface{}

	if err := json.Unmarshal(body, &clientResponse); err != nil {
		panic(err)
	}

	var downloads []Download
	for _, downloadMap := range clientResponse {

		paused := downloadMap["state"] == "pausedUP" || downloadMap["state"] == "missingFiles"
		active := downloadMap["state"] == "queuedUP" || downloadMap["state"] == "uploading" || downloadMap["state"] == "stalledUP"

		// only include downloads with either of these states
		if !active && !paused {
			continue
		}

		newDowload := Download{
			Raw:         downloadMap,
			Active:      active,
			Category:    downloadMap["category"].(string),
			ContentPath: downloadMap["content_path"].(string),
			Hash:        downloadMap["hash"].(string),
			Name:        downloadMap["name"].(string),
			Tag:         downloadMap["tags"].(string),
			Size:        downloadMap["total_size"].(float64),
		}
		downloads = append(downloads, newDowload)
	}

	return DownloadCollection{Downloads: downloads}
}

type Download struct {
	Active      bool
	Category    string
	ContentPath string
	Hash        string
	Name        string
	Raw         map[string]interface{}
	Size        float64
	Tag         string
}

type DownloadCollection struct {
	Downloads []Download
}
type Config struct {
	AllowByCategory           string
	AllowByName               string
	AllowByTag                string
	ConsiderIgnoredInPoolSize bool
	DoNotChangeDownloadClient bool
	DoNotChangeFiles          bool
	DoNotDestroyFiles         bool
	DownloadClientUrl         string
	IgnoreByCategory          string
	IgnoreByName              string
	IgnoreByTag               string
	PoolSize                  float64
	RcloneRemote              string
	RecheckOnResume           bool
	RemoveOnlyWhenPoolIsFull  bool
	RemoveConditionInclusive  bool
	RemoveConditions          []Condition
	SortField                 string
	SortInvertOrder           bool
	UseStash                  bool
}

type Condition struct {
	Field  string
	Invert bool
	Value  float64
}
