import json
from collections import defaultdict, Counter

cands = []
for line in open('/Users/g/git/deepfry/web-of-trust/.planning/spikes/spam-clusters/_raw_band.json'):
    line=line.strip()
    if not line: continue
    d=json.loads(line)
    cands.extend(d['data']['candidates'])

# dedup by pubkey
seen={}
for c in cands:
    seen[c['pubkey']]=c
cands=list(seen.values())

def analyze(T):
    flagged=[]
    for c in cands:
        inb=c.get('inbound',[])
        if not inb: continue
        fcs=[x.get('fc',0) for x in inb]
        maxfc=max(fcs)
        frac=sum(1 for f in fcs if f>=T)/len(fcs)
        if maxfc<T:
            flagged.append({'pubkey':c['pubkey'],'follower_count':c['follower_count'],
                'n_inbound':len(inb),'max_inbound_fc':maxfc,'frac_ge_T':frac,
                'inbound_pks':[x.get('ipk') for x in inb if x.get('ipk')]})
    return flagged

total=len(cands)
with_inb=sum(1 for c in cands if c.get('inbound'))
fc_dist=Counter(c['follower_count'] for c in cands)
f200=analyze(200); f500=analyze(500)
print("Total unique candidates:",total)
print("fc distribution:",dict(sorted(fc_dist.items())))
print("with inbound:",with_inb,"zero inbound:",total-with_inb)
print("Flagged T=200:",len(f200),f"({100*len(f200)/total:.1f}%)")
print("Flagged T=500:",len(f500),f"({100*len(f500)/total:.1f}%)")

# pod detection on T=200 flagged
fol2c=defaultdict(set); c2f={}
for fc in f200:
    s=set(fc['inbound_pks']); c2f[fc['pubkey']]=s
    for p in s: fol2c[p].add(fc['pubkey'])
pod_fol={p:len(cs) for p,cs in fol2c.items() if len(cs)>=5}
top=sorted(pod_fol.items(),key=lambda x:-x[1])[:15]
print("\nTop shared inbound followers (-> #flagged followed):")
for p,n in top: print(f"  {p} -> {n}")

parent={fc['pubkey']:fc['pubkey'] for fc in f200}
def find(x):
    while parent[x]!=x: parent[x]=parent[parent[x]]; x=parent[x]
    return x
def union(a,b):
    ra,rb=find(a),find(b)
    if ra!=rb: parent[ra]=rb
for p in pod_fol:
    m=list(fol2c[p])
    for a,b in zip(m,m[1:]): union(a,b)
comp=defaultdict(list)
for pk in c2f: comp[find(pk)].append(pk)
pods=sorted([v for v in comp.values() if len(v)>=3],key=lambda x:-len(x))
print(f"\nPods >=3 members: {len(pods)}; largest: {len(pods[0]) if pods else 0}")
pod_detail=[]
for i,p in enumerate(pods[:10]):
    fols=Counter()
    for m in p:
        for f in c2f[m]: fols[f]+=1
    common=[f for f,n in fols.items() if n>=max(2,len(p)*0.5)]
    pod_detail.append({'size':len(p),'shared_followers':len(common),
        'top_shared':[f for f,_ in sorted(fols.items(),key=lambda x:-x[1])[:3]],
        'members':p[:10]})
    print(f"  Pod {i+1}: {len(p)} members, {len(common)} followers shared by >=50%")

strong=sorted(f200,key=lambda x:(x['max_inbound_fc'],-x['follower_count']))
table=[(s['pubkey'],s['follower_count'],s['max_inbound_fc'],round(100*s['frac_ge_T'],1),s['n_inbound']) for s in strong[:25]]

json.dump({'total':total,'fc_dist':dict(sorted(fc_dist.items())),'with_inbound':with_inb,
    'f200':len(f200),'f500':len(f500),'top_pod_followers':top,
    'pods':pod_detail,'n_pods':len(pods),'largest_pod':len(pods[0]) if pods else 0,
    'table':table},
    open('/Users/g/git/deepfry/web-of-trust/.planning/spikes/spam-clusters/_results.json','w'),indent=2)
print("\nDone.")
