kubectl logs -l app=godel-scheduler -n godel-system --tail 100 | grep -E "divideNodesByRequireAffinity input nodeCircle|TopologyElem after sort"
