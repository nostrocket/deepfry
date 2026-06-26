import json, itertools
from collections import defaultdict, Counter

d = json.load(open('/Users/g/git/deepfry/web-of-trust/.planning/spikes/spam-clusters/_raw_q2.json'))
cands = d['data']['candidates']

def analyze(T):
    flagged = []
    for c in cands:
        inb = c.get('inbound', [])
        if not inb:
            continue  # no inbound followers at all (~follows empty) -> skip; not a pod member with internal edges
        fcs = [x.get('fc', 0) for x in inb]
        maxfc = max(fcs) if fcs else 0
        frac_ge = sum(1 for f in fcs if f >= T) / len(fcs)
        if maxfc < T:
            flagged.append({
                'pubkey': c['pubkey'],
                'follower_count': c['follower_count'],
                'n_inbound': len(inb),
                'max_inbound_fc': maxfc,
                'frac_ge_T': frac_ge,
                'inbound_pks': [x.get('ipk') for x in inb if x.get('ipk')],
            })
    return flagged

total = len(cands)
with_inbound = sum(1 for c in cands if c.get('inbound'))
no_inbound = total - with_inbound

f200 = analyze(200)
f500 = analyze(500)

print(f"Total candidates sampled: {total}")
print(f"  with >=1 inbound (~follows) edge: {with_inbound}")
print(f"  with zero inbound edges (skipped): {no_inbound}")
print(f"Flagged at T=200: {len(f200)}")
print(f"Flagged at T=500: {len(f500)}")

# follower_count distribution of candidates
fc_dist = Counter(c['follower_count'] for c in cands)
print("fc band:", min(fc_dist), "-", max(fc_dist))

# Pod detection via shared inbound followers among T=200 flagged set.
# Build map: inbound follower pubkey -> set of flagged candidates it follows
flagged_set = f200
fol_to_cands = defaultdict(set)
cand_to_fols = {}
for fc in flagged_set:
    s = set(fc['inbound_pks'])
    cand_to_fols[fc['pubkey']] = s
    for p in s:
        fol_to_cands[p].add(fc['pubkey'])

# A "pod follower" = an inbound follower that follows many flagged candidates
pod_followers = {p: len(cs) for p, cs in fol_to_cands.items() if len(cs) >= 5}
top_pod_followers = sorted(pod_followers.items(), key=lambda x: -x[1])[:15]
print("\nTop shared inbound followers (follower_pubkey -> #flagged candidates it follows):")
for p, n in top_pod_followers:
    print(f"  {p[:16]}... -> {n}")

# Build pod clusters: connected components where candidates share inbound followers
# Use union-find over flagged candidates linked if they share >=2 common inbound followers
parent = {fc['pubkey']: fc['pubkey'] for fc in flagged_set}
def find(x):
    while parent[x]!=x:
        parent[x]=parent[parent[x]]; x=parent[x]
    return x
def union(a,b):
    ra,rb=find(a),find(b)
    if ra!=rb: parent[ra]=rb

pks = list(cand_to_fols.keys())
# only link via shared "pod followers" (followers that connect >=5 candidates) to keep it meaningful
big_fols = set(pod_followers.keys())
for p in big_fols:
    members = list(fol_to_cands[p])
    for a,b in zip(members, members[1:]):
        union(a,b)

comp = defaultdict(list)
for pk in pks:
    comp[find(pk)].append(pk)
pods = sorted(comp.values(), key=lambda x: -len(x))
print(f"\nApparent pods (linked by shared pod-followers, >=3 members): ")
big_pods = [p for p in pods if len(p)>=3]
for i,p in enumerate(big_pods[:10]):
    # shared followers count for this pod
    fols = Counter()
    for m in p:
        for f in cand_to_fols[m]:
            fols[f]+=1
    common = [f for f,n in fols.items() if n>=len(p)*0.5]
    print(f"  Pod {i+1}: {len(p)} members, {len(common)} followers shared by >=50% of members")
print(f"Total pods >=3 members: {len(big_pods)}")
print(f"Largest pod size: {len(big_pods[0]) if big_pods else 0}")

# Strongest pod candidates table: flagged at T=200, sorted by lowest max_inbound_fc then highest follower_count
strong = sorted(f200, key=lambda x: (x['max_inbound_fc'], -x['follower_count']))
# recompute frac at T=200 already stored
out = []
for s in strong[:25]:
    out.append((s['pubkey'], s['follower_count'], s['max_inbound_fc'], round(s['frac_ge_T'],3), s['n_inbound']))

import json as J
J.dump({
    'total': total, 'with_inbound': with_inbound, 'no_inbound': no_inbound,
    'f200': len(f200), 'f500': len(f500),
    'top_pod_followers': top_pod_followers,
    'pods': [len(p) for p in big_pods],
    'largest_pod': big_pods[0] if big_pods else [],
    'strong_table': out,
}, open('/Users/g/git/deepfry/web-of-trust/.planning/spikes/spam-clusters/_results.json','w'), indent=2)
print("\nStrongest table written.")
