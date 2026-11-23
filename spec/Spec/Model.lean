import Spec.Core

/-! What up's steps act on, and the templates the steps are written with.
Spec/Steps.lean, generated from the step table in
internal/isotty/upsteps.go, lists the steps; the three steps whose proofs
are written by hand (`restoreT`, `homeT`, `opecoT`) are here, and the Go
table names them. -/

inductive Agent | claude | codex
deriving DecidableEq, Repr

def allAgents : List Agent := [.claude, .codex]

inductive Client | epweave | github | gitlab | bitbucket | redmine | docker | google | gas | dns | child
  | ssh | «open» | sync | opeco | audit
deriving DecidableEq, Repr

def allClients : List Client :=
  [.epweave, .github, .gitlab, .bitbucket, .redmine, .docker, .google, .gas, .dns, .child,
   .ssh, .open, .sync, .opeco, .audit]

/-- The paths of an agent's home that its saved state restores. -/
inductive HomePath | claudeDir | claudeJson | codexDir
deriving DecidableEq, Repr

def allPaths : List HomePath := [.claudeDir, .claudeJson, .codexDir]

inductive Res
  | vm | hostState | sshConfig | sshWrappers | workspace | auditd
  | pkg (name : String) | nodeRepo | agentCli (a : Agent) | tool (name : String) | service (name : String)
  | auditArchives | mergeScript | keepalive | tmuxConf | mutagen | postInstall
  | client (c : Client)
  /-- The path exists: what the restore observes. -/
  | pathExists (p : HomePath)
  /-- The saved home is in the path: what the restore means. -/
  | login (p : HomePath)
  | homeFile (p : Option HomePath) (name : String)
  | skill (a : Agent) (c : Client)   -- ~/.claude/skills, ~/.codex/skills
  | hook (a : Agent) (c : Client)    -- ~/.claude/settings.json, ~/.codex/hooks.json
  | mcp (a : Agent) (c : Client)     -- ~/.claude.json, ~/.codex/config.toml
  | notifySetting (a : Agent)        -- ~/.claude/settings.json, ~/.codex/config.toml
deriving DecidableEq, Repr

/-- The home path a thing lives in. -/
def Res.path : Res → Option HomePath
  | .skill .claude _ | .hook .claude _ | .notifySetting .claude => some .claudeDir
  | .mcp .claude _ => some .claudeJson
  | .skill .codex _ | .hook .codex _ | .mcp .codex _ | .notifySetting .codex => some .codexDir
  | .homeFile p _ => p
  | _ => none

/-- Writing a thing creates the home path it lives in. -/
def Res.written (r : Res) : List Res := r :: (r.path.map .pathExists).toList

/-- Things with the agents and the names forgotten: finitely many, so the
order of the steps is checked by computation. -/
inductive Kind
  | vm | hostState | sshConfig | sshWrappers | workspace | auditd
  | pkg | nodeRepo | agentCli | tool | service
  | auditArchives | mergeScript | keepalive | tmuxConf | mutagen | postInstall
  | client (c : Client) | pathExists (p : HomePath) | login (p : HomePath)
  | homeFile (p : Option HomePath)
  | skill (p : HomePath) (c : Client) | hook (p : HomePath) (c : Client)
  | mcp (p : HomePath) (c : Client) | notifySetting (p : HomePath)
deriving DecidableEq, Repr

def Res.kind : Res → Kind
  | .vm => .vm | .hostState => .hostState | .sshConfig => .sshConfig
  | .sshWrappers => .sshWrappers | .workspace => .workspace | .auditd => .auditd
  | .pkg _ => .pkg | .nodeRepo => .nodeRepo | .agentCli _ => .agentCli
  | .tool _ => .tool | .service _ => .service
  | .auditArchives => .auditArchives | .mergeScript => .mergeScript | .keepalive => .keepalive
  | .tmuxConf => .tmuxConf | .mutagen => .mutagen | .postInstall => .postInstall
  | .client c => .client c | .pathExists p => .pathExists p | .login p => .login p
  | .homeFile p _ => .homeFile p
  | r@(.skill _ c) => .skill (r.path.getD .claudeDir) c
  | r@(.hook _ c) => .hook (r.path.getD .claudeDir) c
  | r@(.mcp _ c) => .mcp (r.path.getD .claudeDir) c
  | r@(.notifySetting _) => .notifySetting (r.path.getD .claudeDir)

def everyPath (f : HomePath → Kind) : List Kind := allPaths.map f

/-- Every kind that lives in home path `p`. -/
def Kind.inPath (p : HomePath) : List Kind :=
  allClients.flatMap (fun c => [.skill p c, .hook p c, .mcp p c]) ++ [.notifySetting p, .homeFile (some p)]

def allPathKinds : List Kind := allPaths.flatMap Kind.inPath

theorem path_kind (r : Res) (p : HomePath) (h : r.path = some p) : r.kind ∈ allPathKinds := by
  cases r with
  | skill a c => cases a <;> cases c <;> decide
  | hook a c => cases a <;> cases c <;> decide
  | mcp a c => cases a <;> cases c <;> decide
  | notifySetting a => cases a <;> decide
  | homeFile q n => simp [Res.path] at h; subst h; cases p <;> simp [Res.kind, allPathKinds, allPaths, Kind.inPath]
  | _ => simp [Res.path] at h

/-! ## Shapes: steps with the names forgotten -/

structure Shape where
  ensures : List Kind
  intent : List Kind := []
  clears : List Kind := []
  destroys : List Kind := []
deriving DecidableEq

namespace Shape
def observed (t : Shape) : List Kind := t.ensures.filter (· ∉ t.intent)
def compat (a b : Shape) : Bool :=
  a.ensures.all (fun k => k ∉ b.clears && k ∉ b.destroys) && a.clears.all (· ∉ b.ensures)
def fools (a b : Shape) : Bool := !a.intent.isEmpty && b.ensures.any (· ∈ a.observed)
def ordered (x y : Shape) : Bool := x.compat y && !y.fools x
def spares (b a : Shape) : Bool := a.intent.all (fun k => k ∉ b.clears && k ∉ b.destroys)
end Shape

/-- `g` does nothing beyond what shape `t` says, and observes exactly what
`t` observes. -/
def Sig.within (g : Sig Res) (t : Shape) : Prop :=
  (∀ r ∈ g.ensures, r.kind ∈ t.ensures ∧ (r ∈ g.intent ↔ r.kind ∈ t.intent)) ∧
  (∀ r ∈ g.clears, r.kind ∈ t.clears) ∧ (∀ r, g.destroys r = true → r.kind ∈ t.destroys)

theorem within_compat {g h : Sig Res} {a b : Shape} (hg : g.within a) (hh : h.within b)
    (hab : a.compat b = true) : compat g h := by
  simp only [Shape.compat, Bool.and_eq_true, List.all_eq_true, decide_eq_true_eq] at hab
  refine ⟨fun r hr => ⟨fun hc => ?_, ?_⟩, fun r hr he => ?_⟩
  · exact (hab.1 _ (hg.1 r hr).1).1 (hh.2.1 r hc)
  · cases hd : h.destroys r
    · rfl
    · exact absurd (hh.2.2 r hd) (hab.1 _ (hg.1 r hr).1).2
  · exact hab.2 _ (hg.2.1 r hr) (hh.1 r he).1

theorem within_fools {g h : Sig Res} {a b : Shape} (hg : g.within a) (hh : h.within b)
    (hw : g.wf) (hab : a.fools b = false) : ¬ fools g h := by
  rintro ⟨hn, r, hr, ho⟩
  obtain ⟨i, hi⟩ := List.exists_mem_of_ne_nil _ hn
  have hai : i.kind ∈ a.intent := (hg.1 i (hw.2.1 i hi)).2.mp hi
  simp only [Shape.fools, Bool.and_eq_false_iff, Bool.not_eq_false', List.isEmpty_iff,
    List.any_eq_false, decide_eq_true_eq] at hab
  rcases hab with he | hab
  · simp [he] at hai
  have ⟨ho1, ho2⟩ := List.mem_filter.mp ho
  apply hab _ (hh.1 r hr).1
  refine List.mem_filter.mpr ⟨(hg.1 r ho1).1, ?_⟩
  simp only [decide_eq_true_eq] at ho2 ⊢
  exact fun hk => ho2 ((hg.1 r ho1).2.mpr hk)

theorem within_spares {g h : Sig Res} {a b : Shape} (hg : g.within a) (hh : h.within b)
    (hw : g.wf) (hab : b.spares a = true) : spares h g := by
  intro r hr
  simp only [Shape.spares, List.all_eq_true, Bool.and_eq_true, decide_eq_true_eq] at hab
  have hk : r.kind ∈ a.intent := (hg.1 r (hw.2.1 r hr)).2.mp hr
  exact ⟨fun hc => (hab _ hk).1 (hh.2.1 r hc), by
    cases hd : h.destroys r
    · rfl
    · exact absurd (hh.2.2 r hd) (hab _ hk).2⟩

/-! ## Templates -/

/-- A step as a template: a shape and how a configuration instantiates it. -/
structure Template (C : Type) where
  shape : Shape
  inst : C → List (Sig Res)
  within : ∀ c, ∀ g ∈ inst c, g.within shape
  wf : ∀ c, ∀ g ∈ inst c, g.wf
  self : ∀ c, (inst c).Pairwise ordered
  /-- Whether every instance observes all it makes. -/
  plain : Bool
  isPlain : plain = true → ∀ c, ∀ g ∈ inst c, g.intent = []

/-- A template with at most one instance, which observes all it makes. -/
def Template.one (shape : Shape) (one : C → Option (Sig Res))
    (within : ∀ c, ∀ g ∈ one c, g.within shape)
    (plain : ∀ c, ∀ g ∈ one c, g.intent = [] ∧ ∀ r ∈ g.clears, r ∉ g.ensures) : Template C where
  shape := shape
  inst c := (one c).toList
  within c g hg := within c g (by simpa using hg)
  wf c g hg := by
    have ⟨hi, hc⟩ := plain c g (by simpa using hg)
    exact ⟨hc, by simp [hi], by simp [hi]⟩
  self c := by cases one c <;> simp
  plain := true
  isPlain _ c g hg := (plain c g (by simpa using hg)).1

def plan (ts : List (Template C)) (c : C) : List (Sig Res) := ts.flatMap (fun t => t.inst c)

theorem plan_rules (ts : List (Template C))
    (ho : (ts.map (·.shape)).Pairwise (fun a b => a.ordered b = true))
    (hs : ∀ a ∈ ts.map (·.shape), ∀ b ∈ ts.map (·.shape), b.spares a = true) (c : C) :
    Rules (plan ts c) where
  wf g hg := by
    simp only [plan, List.mem_flatMap] at hg
    obtain ⟨t, _, hg⟩ := hg
    exact t.wf c g hg
  ordered := by
    unfold plan
    rw [List.pairwise_flatMap]
    refine ⟨fun t _ => t.self c, ?_⟩
    refine (List.pairwise_map.mp ho).imp (fun {a b} h x hx y hy => ?_)
    simp only [Shape.ordered, Bool.and_eq_true, Bool.not_eq_true'] at h
    exact ⟨within_compat (a.within c x hx) (b.within c y hy) h.1,
      within_fools (b.within c y hy) (a.within c x hx) (b.wf c y hy) h.2⟩
  spares a ha b hb := by
    simp only [plan, List.mem_flatMap] at ha hb
    obtain ⟨ta, hta, ha⟩ := ha
    obtain ⟨tb, htb, hb⟩ := hb
    exact within_spares (ta.within c a ha) (tb.within c b hb) (ta.wf c a ha)
      (hs _ (List.mem_map_of_mem hta) _ (List.mem_map_of_mem htb))

/-! ## The configuration -/

structure Config where
  agents : List Agent
  /-- Node.js is wanted: by an agent, or by its own setting. -/
  node : Bool
  pkgs : List String
  tools : List String
  services : List String
  clients : List Client
  opeco : Bool
  postInstall : Bool
  /-- The host keeps audit archives of earlier VMs of the project. -/
  archives : Bool
  /-- The home paths the host has a saved state of. -/
  saved : List HomePath
  /-- ~/.isotty/home: each file and the agent home path it lies in, if any. -/
  home : List (Option HomePath × String)

def forAgents (cfg : Config) (f : Agent → List Res) : List Res :=
  cfg.agents.flatMap (fun a => (f a).flatMap Res.written)

theorem mem_forAgents {cfg : Config} {f : Agent → List Res} {r : Res} :
    r ∈ forAgents cfg f → ∃ a, r ∈ (f a).flatMap Res.written := by
  simp only [forAgents, List.mem_flatMap]
  exact fun ⟨a, _, x, hx, h⟩ => ⟨a, x, hx, h⟩

theorem mem_allAgents (a : Agent) : a ∈ allAgents := by cases a <;> decide

theorem within_plain {ens : List Res} {t : Shape} (al : Bool) (ht : t.intent = [])
    (h : ∀ r ∈ ens, r.kind ∈ t.ensures) : Sig.within { ensures := ens, always := al } t :=
  ⟨fun r hr => ⟨h r hr, by simp [ht]⟩, by simp, by simp⟩

/-! ## The templates the generated steps use -/

/-- A step that, when `cond` holds, makes `base` present and, for each
agent, `each a`. -/
def step (base : List Res) (each : Agent → List Res := fun _ => [])
    (cond : Config → Bool := fun _ => true) (rerun : Bool := false) : Template Config :=
  .one ⟨(base ++ allAgents.flatMap (fun a => (each a).flatMap Res.written)).map Res.kind, [], [], []⟩
    (fun cfg => if cond cfg then some { ensures := base ++ forAgents cfg each, always := rerun } else none)
    (fun cfg g hg => by
      split at hg
      · simp at hg; subst hg
        refine within_plain _ rfl (fun r hr => List.mem_map_of_mem ?_)
        rcases List.mem_append.mp hr with hr | hr
        · exact List.mem_append_left _ hr
        · obtain ⟨a, ha⟩ := mem_forAgents hr
          exact List.mem_append_right _ (List.mem_flatMap.mpr ⟨a, mem_allAgents a, ha⟩)
      · simp at hg)
    (fun cfg g hg => by split at hg <;> simp at hg; subst hg; simp)

/-- A step that makes present one thing per name the configuration lists. -/
def perName (k : Kind) (ctor : String → Res) (hk : ∀ n, (ctor n).kind = k)
    (names : Config → List String) (cond : Config → Bool := fun _ => true) : Template Config :=
  .one ⟨[k], [], [], []⟩
    (fun cfg => if cond cfg then some { ensures := (names cfg).map ctor } else none)
    (fun cfg g hg => by
      split at hg
      · simp at hg; subst hg
        exact within_plain _ rfl (fun r hr => by
          simp only [List.mem_map] at hr; obtain ⟨n, _, rfl⟩ := hr; simp [hk])
      · simp at hg)
    (fun cfg g hg => by split at hg <;> simp at hg; subst hg; simp)

/-! ## The three steps written by hand -/

/-- Each path of an agent's saved home, restored when the path is absent:
staged on the home's file system and moved into place as a whole,
replacing whatever was in it. The paths restored are those the host has a
saved state of, whatever agents the configuration has now. -/
def restoreSig (p : HomePath) : Sig Res :=
  { ensures := [.pathExists p, .login p], intent := [.login p], atomic := true,
    destroys := fun r => r.path == some p }

def restoreShape : Shape :=
  { ensures := everyPath .pathExists ++ everyPath .login, intent := everyPath .login,
    destroys := allPathKinds }

theorem restore_within (p : HomePath) : (restoreSig p).within restoreShape := by
  refine ⟨fun r hr => ?_, by simp [restoreSig], fun r hr => path_kind r p (by simpa [restoreSig] using hr)⟩
  simp [restoreSig] at hr
  rcases hr with rfl | rfl <;> cases p <;> decide

theorem restore_ordered : (allPaths.map restoreSig).Pairwise ordered := by decide

def restoreT : Template Config where
  shape := restoreShape
  inst cfg := (allPaths.filter (· ∈ cfg.saved)).map restoreSig
  within cfg g hg := by
    simp only [List.mem_map] at hg
    obtain ⟨p, _, rfl⟩ := hg
    exact restore_within p
  wf cfg g hg := by
    simp only [List.mem_map] at hg
    obtain ⟨p, _, rfl⟩ := hg
    exact ⟨by simp [restoreSig], by simp [restoreSig], fun _ => by simp [restoreSig]⟩
  self cfg := restore_ordered.sublist ((List.filter_sublist).map _)
  plain := false
  isPlain h := nomatch h

/-- ~/.isotty/home, unpacked on every up, after the restore: the same on
every VM. -/
def homeT : Template Config :=
  .one ⟨[.homeFile none] ++ everyPath (.homeFile ∘ some) ++ everyPath .pathExists, [], [], []⟩
    (fun cfg => some { ensures := cfg.home.flatMap (fun (p, n) => (Res.homeFile p n).written), always := true })
    (fun cfg g hg => by
      simp at hg; subst hg
      refine within_plain _ rfl (fun r hr => ?_)
      simp only [List.mem_flatMap] at hr
      obtain ⟨⟨p, n⟩, _, hr⟩ := hr
      cases p with
      | none => simp [Res.written, Res.path] at hr; subst hr; simp [Res.kind]
      | some q =>
        simp [Res.written, Res.path] at hr
        rcases hr with rfl | rfl <;> cases q <;> simp [Res.kind, everyPath, allPaths])
    (fun cfg g hg => by simp at hg; subst hg; simp)

def opecoEach (a : Agent) : List Res := [.skill a .opeco, .hook a .opeco, .mcp a .opeco]

def opecoRes (cfg : Config) : List Res :=
  [.client .opeco, .mergeScript, .pkg "python3-tomli-w"] ++ forAgents cfg opecoEach

def opecoKinds : List Kind :=
  [.client .opeco, .mergeScript, .pkg] ++ everyPath (.skill · .opeco) ++ everyPath (.hook · .opeco) ++
  everyPath (.mcp · .opeco) ++ everyPath .pathExists

theorem opecoRes_kind (cfg : Config) : ∀ r ∈ opecoRes cfg, r.kind ∈ opecoKinds := by
  intro r hr
  simp only [opecoRes, List.mem_append] at hr
  rcases hr with hr | hr
  · simp at hr; rcases hr with rfl | rfl | rfl <;> decide
  · obtain ⟨a, ha⟩ := mem_forAgents hr
    have key : ∀ a, ∀ r ∈ (opecoEach a).flatMap Res.written, r.kind ∈ opecoKinds := by
      intro a; cases a <;> decide
    exact key a r ha

/-- What taking opeco out removes: its skills, hooks and MCP entries, not
the home paths they lie in. -/
def opecoClears (cfg : Config) : List Res := [.client .opeco] ++ cfg.agents.flatMap opecoEach

def opecoClearKinds : List Kind :=
  [.client .opeco] ++ everyPath (.skill · .opeco) ++ everyPath (.hook · .opeco) ++ everyPath (.mcp · .opeco)

/-- opeco is installed when active, and taken out otherwise while the
project has agents. -/
def opecoT : Template Config :=
  { shape := { ensures := opecoKinds, clears := opecoClearKinds }
    inst cfg := if cfg.opeco then [{ ensures := opecoRes cfg, always := true }]
      else if cfg.agents.isEmpty then []
      else [{ ensures := [.mergeScript, .pkg "python3-tomli-w"], clears := opecoClears cfg, always := true }]
    within cfg g hg := by
      split at hg
      · simp at hg; subst hg
        exact ⟨fun r hr => ⟨opecoRes_kind cfg r hr, by simp⟩, by simp, by simp⟩
      · split at hg
        · simp at hg
        · simp at hg; subst hg
          refine ⟨fun r hr => ⟨?_, by simp⟩, fun r hr => ?_, by simp⟩
          · simp at hr; rcases hr with rfl | rfl <;> decide
          · simp only [opecoClears, List.mem_append, List.mem_flatMap] at hr
            rcases hr with hr | ⟨a, _, hr⟩
            · simp at hr; subst hr; decide
            · have key : ∀ a, ∀ r ∈ opecoEach a, r.kind ∈ opecoClearKinds := by
                intro a; cases a <;> decide
              exact key a r hr
    wf cfg g hg := by
      split at hg
      · simp at hg; subst hg; simp [Sig.wf]
      · split at hg
        · simp at hg
        · simp at hg; subst hg
          refine ⟨fun r hr => ?_, by simp, by simp⟩
          simp only [opecoClears, List.mem_append, List.mem_flatMap] at hr
          rcases hr with hr | ⟨a, _, hr⟩
          · simp at hr; subst hr; simp
          · cases a <;> simp [opecoEach] at hr <;> rcases hr with rfl | rfl | rfl <;> simp
    self cfg := by
      split
      · simp
      · split <;> simp
    plain := true
    isPlain _ cfg g hg := by
      split at hg
      · simp at hg; subst hg; rfl
      · split at hg
        · simp at hg
        · simp at hg; subst hg; rfl }
