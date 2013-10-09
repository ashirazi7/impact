import urllib2
import json

from impactlib import config

cached_data = None

def load_repo_data():
    global cached_data

    # If we've already called this function once in this process, use
    # the previously returned result.
    if cached_data!=None:
        return cached_data

    urls = config.get_repos()
    ret = {}
    # Ideally, we could cache this data if we cannot immediately
    # fetch it.  But it isn't too much good since it would only
    # allow you to search.  Install will require network connectivity
    # anyway.
    for url in urls:
        req = urllib2.Request(url)
        response = urllib2.urlopen(req)
        data = json.loads(response.read())
        ret.update(data)

    cached_data = ret
    return ret