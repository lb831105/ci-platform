import json, os, shutil, subprocess, time, urllib.request

BASE="http://127.0.0.1:18374"; HERE=os.getcwd(); DH=os.path.join(HERE,".tmp-e2e","data")
BIN=os.path.join("bin","ci-server-eff-e2e.exe")

def call(m,p,b=None,tok=None):
    h={}; d=None
    if b is not None:
        d=json.dumps(b).encode(); h["Content-Type"]="application/json"
    if tok: h["Authorization"]="Bearer "+tok
    r=urllib.request.Request(BASE+p,data=d,headers=h,method=m)
    with urllib.request.urlopen(r,timeout=30) as resp: return json.loads(resp.read())

def start():
    proc=subprocess.Popen([BIN,"-port","18374","-data-home",DH],stdout=subprocess.DEVNULL,stderr=subprocess.STDOUT)
    for _ in range(120):
        try: urllib.request.urlopen(BASE+"/api/health",timeout=1).read(); return proc
        except Exception: time.sleep(0.25)
    raise SystemExit("server did not start")

fails=[]
def check(cond,label,extra=""):
    print(("PASS " if cond else "FAIL ")+label+("" if cond else " :: "+str(extra)[:400]))
    if not cond: fails.append(label)

shutil.rmtree(DH,ignore_errors=True)
proc=start()
try:
    admin=call("POST","/api/auth/login",{"username":"admin","password":"admin"})["data"]["token"]
    for n in ["as_doc_admin","gomysqldump","prod-deploy"]:
        call("POST","/api/pipelines",{"name":n,"steps":[{"name":"s","run":"echo x"}]},tok=admin)

    call("POST","/api/security/users",{"name":"libin","password":"pass12345","email":"l@example.com"},tok=admin)
    call("POST","/api/security/users",{"name":"other","password":"pass12345","email":"o@example.com"},tok=admin)

    roles=[
      # 全局角色只给 administer：按 Jenkins 语义隐含全部权限
      {"name":"admin","kind":"global","permissions":["overall.administer"]},
      # 任务角色：名字与任务名相同
      {"name":"as_doc_admin","kind":"item","pattern":"as_doc_admin","permissions":["job.read"]},
      {"name":"gomysqldump","kind":"item","pattern":"gomysqldump","permissions":["job.read"]},
      # Pattern 为空的任务角色（只能当模板）
      {"name":"tpl-empty","kind":"item","pattern":"","permissions":["job.read"]},
      # Pattern 匹配不到任何任务
      {"name":"typo","kind":"item","pattern":"as-doc-.*","permissions":["job.read"]},
      # 有角色但缺 job.read
      {"name":"build-only","kind":"item","pattern":"prod-.+","permissions":["job.build"]},
    ]
    call("PUT","/api/security/rbac/roles",{"roles":roles},tok=admin)
    asg=[
      {"role":"admin","kind":"global","subject":"libin"},
      {"role":"as_doc_admin","kind":"item","subject":"libin"},
      {"role":"gomysqldump","kind":"item","subject":"libin"},
      {"role":"typo","kind":"item","subject":"other"},
      {"role":"build-only","kind":"item","subject":"other"},
    ]
    call("PUT","/api/security/rbac/assignments",{"assignments":asg},tok=admin)

    eff=call("GET","/api/security/rbac/effective",tok=admin)["data"]
    subs={s["subject"]:s for s in eff["subjects"]}
    roles_by={r["name"]:r for r in eff["roles"]}
    check(sorted(eff["job_names"])==["as_doc_admin","gomysqldump","prod-deploy"],"effective lists all job names",eff["job_names"])

    libin=subs["libin"]
    check(libin["administer"] is True,"libin administer detected",libin)
    check(libin["all_jobs"] is True,"administer => all jobs visible",libin)
    check(sorted(libin["visible_jobs"])==["as_doc_admin","gomysqldump","prod-deploy"],"administer sees every job",libin["visible_jobs"])
    check(len(libin["permissions"])>=13,"administer expands to all permissions",len(libin["permissions"]))

    check(roles_by["as_doc_admin"]["matched_jobs"]==["as_doc_admin"],"item role pattern matches its job",roles_by["as_doc_admin"])
    check(roles_by["tpl-empty"]["matched_jobs"]==[],"empty pattern matches nothing",roles_by["tpl-empty"])
    check(roles_by["typo"]["matched_jobs"]==[],"wrong pattern matches nothing (UI warns)",roles_by["typo"])
    check(roles_by["admin"]["permissions"] and "job.build" in roles_by["admin"]["permissions"],"administer role shows expanded perms",roles_by["admin"]["permissions"][:3])

    other=subs["other"]
    check(other["all_jobs"] is False,"other is scoped",other)
    check(other["visible_jobs"]==[],"build-only role grants no visibility (UI shows warning)",other["visible_jobs"])
    check(len(other["roles"])==2,"other keeps its role list for the warning",other["roles"])

    anon=subs["anonymous"]
    check(anon["roles"]==[],"anonymous has no roles by default",anon)
    check(anon["visible_jobs"]==[],"anonymous sees nothing",anon["visible_jobs"])
finally:
    proc.terminate()
    try: proc.wait(timeout=10)
    except Exception: proc.kill()

print("E2E ALL PASSED" if not fails else "E2E FAILED: "+", ".join(fails))
