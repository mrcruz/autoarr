# autoarr

*Don't look at your torrent client again*

Manages your qBittorrent client automatically. 
Designed to be a companion to Arr applications.

A stateless script that is intended to run every day and selects what your are going to seed based on an arbitrary criteria.
It's goal is to keep a maximum "Pool Size" (In GB) of active torrents, pausing less prioritize downloads and possibly deleting them, if desired.
It can also move *idle downloads* to a remote location in order to save space on the seedbox and retrive then once needed.


## Usage

Below, there is an example of config doing some things:

* Prioritize to seed downloads with the least amount of seeders
* Ignores torrent that contain *My_Special_Download* in the name
* Removes downloads with ratio over 20 *AND* with more than 1000 seeders
* Keeps the active pool size under 100 GB and move the Idle downloads to the Idle Pool

```
{
    "AllowByCategory": "",
    "DoNotChangeFiles": false,
    "DoNotChangeDownloadClient": false,
    "UseStash": true,
    "DownloadCLientUrl": "http://192.168.1.2:8080",
    "IgnoreByCategory": "",
    "IgnoreByName": "My_Special_Download",
    "IgnoreByTag": "",
    "PoolSize": 100,
    "RcloneRemote": "",
    "RemoveConditionInclusive": false,
    "RemoveConditions": [
        {
            "Field": "ratio",
            "Value": 20,
            "Invert": false
        },
        {
            "Field": "num_complete",
            "Value": 1000,
            "Invert": false
        }
    ],
    "SortField": "num_complete",
    "SortInvertOrder": false
}
```

### Docker

This project was design to work mainly as a docker short lived container.
Here is an docker-compose example:

```
version: '3'

services:
  autoarr:
    build: ./
    user: "1000"
    volumes: 
      - ./input.json:/config/input.json
      - ./rclone.conf:/home/user/.config/rclone/rclone.conf
      - <PATH_TO_IDLE_POOL>:/idle
      - <PATH_TO_ACTIVE_POOL>:/active
    network_mode: host
    
```

Then, you would run daily like this:

```
docker-compose run --rm autoarr
```

## Notes

This is a learning exercise tool and my first Go project. 
Feel free to make pull requests.

It uses [rclone](https://github.com/rclone/rclone) to move files so you can have any backend that rclone supports as your *Idle pool*.

### Future

* More complex priority options
* Support other torrent clients
* Auto manage idle pool
* Support more use cases