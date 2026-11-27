export namespace a2a {
	
	export class AgentCapabilities {
	    streaming: boolean;
	    pushNotifications: boolean;
	    stateTransitionHistory: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AgentCapabilities(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.streaming = source["streaming"];
	        this.pushNotifications = source["pushNotifications"];
	        this.stateTransitionHistory = source["stateTransitionHistory"];
	    }
	}
	export class AgentSkill {
	    id: string;
	    name: string;
	    description: string;
	    tags?: string[];
	    examples?: string[];
	    inputModes?: string[];
	    outputModes?: string[];
	
	    static createFrom(source: any = {}) {
	        return new AgentSkill(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.tags = source["tags"];
	        this.examples = source["examples"];
	        this.inputModes = source["inputModes"];
	        this.outputModes = source["outputModes"];
	    }
	}
	export class AgentProvider {
	    organization: string;
	    url?: string;
	
	    static createFrom(source: any = {}) {
	        return new AgentProvider(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.organization = source["organization"];
	        this.url = source["url"];
	    }
	}
	export class AgentCard {
	    name: string;
	    description: string;
	    url: string;
	    version: string;
	    protocolVersion: string;
	    provider?: AgentProvider;
	    capabilities: AgentCapabilities;
	    defaultInputModes: string[];
	    defaultOutputModes: string[];
	    skills: AgentSkill[];
	    metadata?: Record<string, any>;
	
	    static createFrom(source: any = {}) {
	        return new AgentCard(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.url = source["url"];
	        this.version = source["version"];
	        this.protocolVersion = source["protocolVersion"];
	        this.provider = this.convertValues(source["provider"], AgentProvider);
	        this.capabilities = this.convertValues(source["capabilities"], AgentCapabilities);
	        this.defaultInputModes = source["defaultInputModes"];
	        this.defaultOutputModes = source["defaultOutputModes"];
	        this.skills = this.convertValues(source["skills"], AgentSkill);
	        this.metadata = source["metadata"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	export class FileContent {
	    name?: string;
	    mimeType?: string;
	    uri?: string;
	    bytes?: string;
	
	    static createFrom(source: any = {}) {
	        return new FileContent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.mimeType = source["mimeType"];
	        this.uri = source["uri"];
	        this.bytes = source["bytes"];
	    }
	}
	export class Part {
	    kind: string;
	    text?: string;
	    file?: FileContent;
	    data?: Record<string, any>;
	    metadata?: Record<string, any>;
	
	    static createFrom(source: any = {}) {
	        return new Part(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.text = source["text"];
	        this.file = this.convertValues(source["file"], FileContent);
	        this.data = source["data"];
	        this.metadata = source["metadata"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Artifact {
	    artifactId: string;
	    name?: string;
	    description?: string;
	    parts: Part[];
	    metadata?: Record<string, any>;
	
	    static createFrom(source: any = {}) {
	        return new Artifact(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.artifactId = source["artifactId"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.parts = this.convertValues(source["parts"], Part);
	        this.metadata = source["metadata"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class Message {
	    messageId: string;
	    role: string;
	    parts: Part[];
	    taskId?: string;
	    contextId?: string;
	    referenceTaskIds?: string[];
	    metadata?: Record<string, any>;
	    // Go type: time
	    timestamp: any;
	
	    static createFrom(source: any = {}) {
	        return new Message(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.messageId = source["messageId"];
	        this.role = source["role"];
	        this.parts = this.convertValues(source["parts"], Part);
	        this.taskId = source["taskId"];
	        this.contextId = source["contextId"];
	        this.referenceTaskIds = source["referenceTaskIds"];
	        this.metadata = source["metadata"];
	        this.timestamp = this.convertValues(source["timestamp"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class TaskStatus {
	    state: string;
	    message?: Message;
	    // Go type: time
	    timestamp: any;
	
	    static createFrom(source: any = {}) {
	        return new TaskStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = source["state"];
	        this.message = this.convertValues(source["message"], Message);
	        this.timestamp = this.convertValues(source["timestamp"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Task {
	    id: string;
	    contextId: string;
	    status: TaskStatus;
	    history?: Message[];
	    artifacts?: Artifact[];
	    metadata?: Record<string, any>;
	    agent: string;
	
	    static createFrom(source: any = {}) {
	        return new Task(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.contextId = source["contextId"];
	        this.status = this.convertValues(source["status"], TaskStatus);
	        this.history = this.convertValues(source["history"], Message);
	        this.artifacts = this.convertValues(source["artifacts"], Artifact);
	        this.metadata = source["metadata"];
	        this.agent = source["agent"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace agent {
	
	export class Definition {
	    name: string;
	    description: string;
	    runtime?: string;
	    model?: string;
	    reasoningEffort?: string;
	    permission: string;
	    skills?: string[];
	    taskKinds?: string[];
	    tags?: string[];
	    maxTurns?: number;
	    secrets?: string[];
	    instructions: string;
	    source: string;
	    path?: string;
	    auto?: boolean;
	    mcp?: string[];
	    tracker?: string[];
	    extra?: string;
	
	    static createFrom(source: any = {}) {
	        return new Definition(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.runtime = source["runtime"];
	        this.model = source["model"];
	        this.reasoningEffort = source["reasoningEffort"];
	        this.permission = source["permission"];
	        this.skills = source["skills"];
	        this.taskKinds = source["taskKinds"];
	        this.tags = source["tags"];
	        this.maxTurns = source["maxTurns"];
	        this.secrets = source["secrets"];
	        this.instructions = source["instructions"];
	        this.source = source["source"];
	        this.path = source["path"];
	        this.auto = source["auto"];
	        this.mcp = source["mcp"];
	        this.tracker = source["tracker"];
	        this.extra = source["extra"];
	    }
	}

}

export namespace app {
	
	export class AgentCandidateView {
	    name: string;
	    description: string;
	    permission: string;
	    runtime?: string;
	    model?: string;
	    source: string;
	    taskKinds?: string[];
	
	    static createFrom(source: any = {}) {
	        return new AgentCandidateView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.permission = source["permission"];
	        this.runtime = source["runtime"];
	        this.model = source["model"];
	        this.source = source["source"];
	        this.taskKinds = source["taskKinds"];
	    }
	}
	export class AgentLocationsView {
	    repository?: string;
	    global?: string;
	
	    static createFrom(source: any = {}) {
	        return new AgentLocationsView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.repository = source["repository"];
	        this.global = source["global"];
	    }
	}
	export class AgentView {
	    definition: agent.Definition;
	    card: a2a.AgentCard;
	    overrides?: string;
	
	    static createFrom(source: any = {}) {
	        return new AgentView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.definition = this.convertValues(source["definition"], agent.Definition);
	        this.card = this.convertValues(source["card"], a2a.AgentCard);
	        this.overrides = source["overrides"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ArtifactView {
	    digest: string;
	    taskId: string;
	    executionId?: string;
	    stepId?: string;
	    agent?: string;
	    kind: string;
	    name: string;
	    mediaType: string;
	    size: number;
	    description?: string;
	    // Go type: time
	    createdAt: any;
	    content?: string;
	    truncated?: boolean;
	    binary?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ArtifactView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.digest = source["digest"];
	        this.taskId = source["taskId"];
	        this.executionId = source["executionId"];
	        this.stepId = source["stepId"];
	        this.agent = source["agent"];
	        this.kind = source["kind"];
	        this.name = source["name"];
	        this.mediaType = source["mediaType"];
	        this.size = source["size"];
	        this.description = source["description"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
	        this.content = source["content"];
	        this.truncated = source["truncated"];
	        this.binary = source["binary"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class WriteAccessView {
	    status: string;
	    detail: string;
	
	    static createFrom(source: any = {}) {
	        return new WriteAccessView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = source["status"];
	        this.detail = source["detail"];
	    }
	}
	export class ProviderAuthenticationView {
	    provider: string;
	    authenticated: boolean;
	    account?: string;
	    detail: string;
	    write?: WriteAccessView;
	
	    static createFrom(source: any = {}) {
	        return new ProviderAuthenticationView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.provider = source["provider"];
	        this.authenticated = source["authenticated"];
	        this.account = source["account"];
	        this.detail = source["detail"];
	        this.write = this.convertValues(source["write"], WriteAccessView);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AuthenticationView {
	    github: ProviderAuthenticationView;
	    claude: ProviderAuthenticationView;
	    linear: ProviderAuthenticationView;
	
	    static createFrom(source: any = {}) {
	        return new AuthenticationView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.github = this.convertValues(source["github"], ProviderAuthenticationView);
	        this.claude = this.convertValues(source["claude"], ProviderAuthenticationView);
	        this.linear = this.convertValues(source["linear"], ProviderAuthenticationView);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class BranchCommit {
	    hash: string;
	    shortHash: string;
	    subject: string;
	    body?: string;
	    author: string;
	    date: string;
	    stepId?: string;
	    agent?: string;
	    external?: boolean;
	    merge?: boolean;
	    files: number;
	    additions: number;
	    deletions: number;
	
	    static createFrom(source: any = {}) {
	        return new BranchCommit(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hash = source["hash"];
	        this.shortHash = source["shortHash"];
	        this.subject = source["subject"];
	        this.body = source["body"];
	        this.author = source["author"];
	        this.date = source["date"];
	        this.stepId = source["stepId"];
	        this.agent = source["agent"];
	        this.external = source["external"];
	        this.merge = source["merge"];
	        this.files = source["files"];
	        this.additions = source["additions"];
	        this.deletions = source["deletions"];
	    }
	}
	export class BranchOutcome {
	    runId: string;
	    name: string;
	    commit?: string;
	    unique: number;
	    deleted: boolean;
	    reason?: string;
	
	    static createFrom(source: any = {}) {
	        return new BranchOutcome(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.runId = source["runId"];
	        this.name = source["name"];
	        this.commit = source["commit"];
	        this.unique = source["unique"];
	        this.deleted = source["deleted"];
	        this.reason = source["reason"];
	    }
	}
	export class BranchPreview {
	    projectId: string;
	    name: string;
	    commit: string;
	    unique: number;
	    deletable: boolean;
	    reason?: string;
	
	    static createFrom(source: any = {}) {
	        return new BranchPreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.projectId = source["projectId"];
	        this.name = source["name"];
	        this.commit = source["commit"];
	        this.unique = source["unique"];
	        this.deletable = source["deletable"];
	        this.reason = source["reason"];
	    }
	}
	export class SettingsShare {
	    mode: string;
	    base: string;
	    commit: string;
	    files: string[];
	    branch?: string;
	    url?: string;
	    pullRequest?: boolean;
	    updated?: worktree.BranchUpdate;
	    note?: string;
	
	    static createFrom(source: any = {}) {
	        return new SettingsShare(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mode = source["mode"];
	        this.base = source["base"];
	        this.commit = source["commit"];
	        this.files = source["files"];
	        this.branch = source["branch"];
	        this.url = source["url"];
	        this.pullRequest = source["pullRequest"];
	        this.updated = this.convertValues(source["updated"], worktree.BranchUpdate);
	        this.note = source["note"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class BranchSyncView {
	    projectId: string;
	    sync: worktree.SyncStatus;
	    settings?: worktree.SettingsStatus;
	    fetchError?: string;
	    update?: worktree.BranchUpdate;
	    deleted?: worktree.BranchDeletion;
	    shared?: SettingsShare;
	
	    static createFrom(source: any = {}) {
	        return new BranchSyncView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.projectId = source["projectId"];
	        this.sync = this.convertValues(source["sync"], worktree.SyncStatus);
	        this.settings = this.convertValues(source["settings"], worktree.SettingsStatus);
	        this.fetchError = source["fetchError"];
	        this.update = this.convertValues(source["update"], worktree.BranchUpdate);
	        this.deleted = this.convertValues(source["deleted"], worktree.BranchDeletion);
	        this.shared = this.convertValues(source["shared"], SettingsShare);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class CapabilityView {
	    id: string;
	    label: string;
	    extension: string;
	
	    static createFrom(source: any = {}) {
	        return new CapabilityView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.label = source["label"];
	        this.extension = source["extension"];
	    }
	}
	export class ProductView {
	    name: string;
	    version: string;
	    inkuna: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ProductView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.version = source["version"];
	        this.inkuna = source["inkuna"];
	    }
	}
	export class CapabilitiesView {
	    product: ProductView;
	    capabilities: CapabilityView[];
	
	    static createFrom(source: any = {}) {
	        return new CapabilitiesView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.product = this.convertValues(source["product"], ProductView);
	        this.capabilities = this.convertValues(source["capabilities"], CapabilityView);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class CardChange {
	    field: string;
	    before: string;
	    after: string;
	
	    static createFrom(source: any = {}) {
	        return new CardChange(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.field = source["field"];
	        this.before = source["before"];
	        this.after = source["after"];
	    }
	}
	export class CatalogEntry {
	    item: catalog.Item;
	    repository?: string;
	    global?: string;
	    globalRefusal?: string;
	
	    static createFrom(source: any = {}) {
	        return new CatalogEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.item = this.convertValues(source["item"], catalog.Item);
	        this.repository = source["repository"];
	        this.global = source["global"];
	        this.globalRefusal = source["globalRefusal"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class CatalogListing {
	    items: CatalogEntry[];
	    repositoryProblem?: string;
	    globalProblem?: string;
	    sources?: sources.Report[];
	
	    static createFrom(source: any = {}) {
	        return new CatalogListing(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.items = this.convertValues(source["items"], CatalogEntry);
	        this.repositoryProblem = source["repositoryProblem"];
	        this.globalProblem = source["globalProblem"];
	        this.sources = this.convertValues(source["sources"], sources.Report);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class CheckLogView {
	    text: string;
	    offset: number;
	    end: number;
	    reset: boolean;
	    running: boolean;
	    cut: boolean;
	    bytes: number;
	
	    static createFrom(source: any = {}) {
	        return new CheckLogView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.text = source["text"];
	        this.offset = source["offset"];
	        this.end = source["end"];
	        this.reset = source["reset"];
	        this.running = source["running"];
	        this.cut = source["cut"];
	        this.bytes = source["bytes"];
	    }
	}
	export class CheckTotals {
	    passed: number;
	    failed: number;
	    running: number;
	    total: number;
	
	    static createFrom(source: any = {}) {
	        return new CheckTotals(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.passed = source["passed"];
	        this.failed = source["failed"];
	        this.running = source["running"];
	        this.total = source["total"];
	    }
	}
	export class CheckView {
	    id: string;
	    executionId: string;
	    stepId: string;
	    stepRunId: string;
	    position: number;
	    argv: string[];
	    status: string;
	    exitCode?: number;
	    // Go type: time
	    startedAt?: any;
	    // Go type: time
	    finishedAt?: any;
	    logDigest?: string;
	    logBytes: number;
	    logCut?: boolean;
	    error?: string;
	    stepTitle?: string;
	    durationMs: number;
	
	    static createFrom(source: any = {}) {
	        return new CheckView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.executionId = source["executionId"];
	        this.stepId = source["stepId"];
	        this.stepRunId = source["stepRunId"];
	        this.position = source["position"];
	        this.argv = source["argv"];
	        this.status = source["status"];
	        this.exitCode = source["exitCode"];
	        this.startedAt = this.convertValues(source["startedAt"], null);
	        this.finishedAt = this.convertValues(source["finishedAt"], null);
	        this.logDigest = source["logDigest"];
	        this.logBytes = source["logBytes"];
	        this.logCut = source["logCut"];
	        this.error = source["error"];
	        this.stepTitle = source["stepTitle"];
	        this.durationMs = source["durationMs"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class CommentDraft {
	    id?: string;
	    threadId?: string;
	    path?: string;
	    side?: string;
	    line?: number;
	    endLine?: number;
	    commit?: string;
	    body: string;
	    target: string;
	
	    static createFrom(source: any = {}) {
	        return new CommentDraft(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.threadId = source["threadId"];
	        this.path = source["path"];
	        this.side = source["side"];
	        this.line = source["line"];
	        this.endLine = source["endLine"];
	        this.commit = source["commit"];
	        this.body = source["body"];
	        this.target = source["target"];
	    }
	}
	export class CommentThreadView {
	    id: string;
	    path: string;
	    side: string;
	    line: number;
	    endLine: number;
	    round: number;
	    state: string;
	    resolvedBy?: string;
	    outdated?: boolean;
	    outdatedRound?: number;
	    unplaced?: boolean;
	    comments: storage.Comment[];
	
	    static createFrom(source: any = {}) {
	        return new CommentThreadView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.path = source["path"];
	        this.side = source["side"];
	        this.line = source["line"];
	        this.endLine = source["endLine"];
	        this.round = source["round"];
	        this.state = source["state"];
	        this.resolvedBy = source["resolvedBy"];
	        this.outdated = source["outdated"];
	        this.outdatedRound = source["outdatedRound"];
	        this.unplaced = source["unplaced"];
	        this.comments = this.convertValues(source["comments"], storage.Comment);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ReviewTarget {
	    id: string;
	    title: string;
	    agent: string;
	
	    static createFrom(source: any = {}) {
	        return new ReviewTarget(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.agent = source["agent"];
	    }
	}
	export class CommentsView {
	    threads: CommentThreadView[];
	    targets: ReviewTarget[];
	    refusal?: string;
	    to?: string;
	
	    static createFrom(source: any = {}) {
	        return new CommentsView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.threads = this.convertValues(source["threads"], CommentThreadView);
	        this.targets = this.convertValues(source["targets"], ReviewTarget);
	        this.refusal = source["refusal"];
	        this.to = source["to"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class CommitView {
	    hash: string;
	    shortHash: string;
	    author: string;
	    date: string;
	    subject: string;
	    body?: string;
	    stat: string;
	    stepId?: string;
	
	    static createFrom(source: any = {}) {
	        return new CommitView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hash = source["hash"];
	        this.shortHash = source["shortHash"];
	        this.author = source["author"];
	        this.date = source["date"];
	        this.subject = source["subject"];
	        this.body = source["body"];
	        this.stat = source["stat"];
	        this.stepId = source["stepId"];
	    }
	}
	export class RepositoryTrackerView {
	    path: string;
	    name: string;
	    tracker?: string;
	    file: string;
	    exists: boolean;
	    project?: string;
	    ceiling: tracker.Allowance;
	    effective: tracker.Allowance;
	    note?: string;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new RepositoryTrackerView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.name = source["name"];
	        this.tracker = source["tracker"];
	        this.file = source["file"];
	        this.exists = source["exists"];
	        this.project = source["project"];
	        this.ceiling = this.convertValues(source["ceiling"], tracker.Allowance);
	        this.effective = this.convertValues(source["effective"], tracker.Allowance);
	        this.note = source["note"];
	        this.error = source["error"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ConfigurationView {
	    path: string;
	    exists: boolean;
	    dataDir: string;
	    file: config.File;
	    secrets: secrets.Status[];
	    defaults: config.File;
	    overrides: config.Override[];
	    routingDefaults: routing.Policy;
	    pricingDefaults: pricing.Table;
	    restartRequired?: boolean;
	    trackers: RepositoryTrackerView[];
	    stageDirectories: string[];
	    libraryProblems: rules.LibraryProblem[];
	    build: version.Info;
	
	    static createFrom(source: any = {}) {
	        return new ConfigurationView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.exists = source["exists"];
	        this.dataDir = source["dataDir"];
	        this.file = this.convertValues(source["file"], config.File);
	        this.secrets = this.convertValues(source["secrets"], secrets.Status);
	        this.defaults = this.convertValues(source["defaults"], config.File);
	        this.overrides = this.convertValues(source["overrides"], config.Override);
	        this.routingDefaults = this.convertValues(source["routingDefaults"], routing.Policy);
	        this.pricingDefaults = this.convertValues(source["pricingDefaults"], pricing.Table);
	        this.restartRequired = source["restartRequired"];
	        this.trackers = this.convertValues(source["trackers"], RepositoryTrackerView);
	        this.stageDirectories = source["stageDirectories"];
	        this.libraryProblems = this.convertValues(source["libraryProblems"], rules.LibraryProblem);
	        this.build = this.convertValues(source["build"], version.Info);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SetupView {
	    configPath: string;
	    configExists: boolean;
	    dataDir: string;
	
	    static createFrom(source: any = {}) {
	        return new SetupView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.configPath = source["configPath"];
	        this.configExists = source["configExists"];
	        this.dataDir = source["dataDir"];
	    }
	}
	export class DefaultsView {
	    maxConcurrency: number;
	    maxTasks: number;
	    planningSeconds: number;
	    workerSeconds: number;
	    verifierSeconds: number;
	    approvalSeconds: number;
	    orchestrator: string;
	    routingPolicy: routing.Policy;
	
	    static createFrom(source: any = {}) {
	        return new DefaultsView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.maxConcurrency = source["maxConcurrency"];
	        this.maxTasks = source["maxTasks"];
	        this.planningSeconds = source["planningSeconds"];
	        this.workerSeconds = source["workerSeconds"];
	        this.verifierSeconds = source["verifierSeconds"];
	        this.approvalSeconds = source["approvalSeconds"];
	        this.orchestrator = source["orchestrator"];
	        this.routingPolicy = this.convertValues(source["routingPolicy"], routing.Policy);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class HealthCheck {
	    name: string;
	    status: string;
	    detail: string;
	    required: boolean;
	    action?: string;
	
	    static createFrom(source: any = {}) {
	        return new HealthCheck(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.status = source["status"];
	        this.detail = source["detail"];
	        this.required = source["required"];
	        this.action = source["action"];
	    }
	}
	export class ProjectView {
	    id: string;
	    name: string;
	    repository: string;
	    defaultRuntime: string;
	    createdAt: string;
	    removedAt?: string;
	
	    static createFrom(source: any = {}) {
	        return new ProjectView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.repository = source["repository"];
	        this.defaultRuntime = source["defaultRuntime"];
	        this.createdAt = source["createdAt"];
	        this.removedAt = source["removedAt"];
	    }
	}
	export class Dashboard {
	    projects: ProjectView[];
	    removedProjects: ProjectView[];
	    health: HealthCheck[];
	    defaults: DefaultsView;
	    setup: SetupView;
	    pendingInputs: number;
	    activeRuns: number;
	
	    static createFrom(source: any = {}) {
	        return new Dashboard(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.projects = this.convertValues(source["projects"], ProjectView);
	        this.removedProjects = this.convertValues(source["removedProjects"], ProjectView);
	        this.health = this.convertValues(source["health"], HealthCheck);
	        this.defaults = this.convertValues(source["defaults"], DefaultsView);
	        this.setup = this.convertValues(source["setup"], SetupView);
	        this.pendingInputs = source["pendingInputs"];
	        this.activeRuns = source["activeRuns"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class DefinitionUser {
	    project: string;
	    repository: string;
	    flows?: string[];
	    problem?: string;
	
	    static createFrom(source: any = {}) {
	        return new DefinitionUser(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.project = source["project"];
	        this.repository = source["repository"];
	        this.flows = source["flows"];
	        this.problem = source["problem"];
	    }
	}
	export class DeleteOptions {
	    removeWorktrees: boolean;
	    deleteBranches?: Record<string, any>;
	
	    static createFrom(source: any = {}) {
	        return new DeleteOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.removeWorktrees = source["removeWorktrees"];
	        this.deleteBranches = source["deleteBranches"];
	    }
	}
	export class DroppedLink {
	    tracker: string;
	    id: string;
	    url?: string;
	
	    static createFrom(source: any = {}) {
	        return new DroppedLink(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tracker = source["tracker"];
	        this.id = source["id"];
	        this.url = source["url"];
	    }
	}
	export class WorktreeOutcome {
	    runId: string;
	    path: string;
	    removed: boolean;
	    reason?: string;
	
	    static createFrom(source: any = {}) {
	        return new WorktreeOutcome(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.runId = source["runId"];
	        this.path = source["path"];
	        this.removed = source["removed"];
	        this.reason = source["reason"];
	    }
	}
	export class TaskDeleteReport {
	    taskId: string;
	    runId?: string;
	    rows: storage.TableRows[];
	    blobs: number;
	    blobBytes: number;
	    keptBlobs: number;
	    worktrees: WorktreeOutcome[];
	    branches: BranchOutcome[];
	    links: DroppedLink[];
	    problems?: string[];
	
	    static createFrom(source: any = {}) {
	        return new TaskDeleteReport(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.taskId = source["taskId"];
	        this.runId = source["runId"];
	        this.rows = this.convertValues(source["rows"], storage.TableRows);
	        this.blobs = source["blobs"];
	        this.blobBytes = source["blobBytes"];
	        this.keptBlobs = source["keptBlobs"];
	        this.worktrees = this.convertValues(source["worktrees"], WorktreeOutcome);
	        this.branches = this.convertValues(source["branches"], BranchOutcome);
	        this.links = this.convertValues(source["links"], DroppedLink);
	        this.problems = source["problems"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class DeleteReport {
	    tasks: TaskDeleteReport[];
	
	    static createFrom(source: any = {}) {
	        return new DeleteReport(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tasks = this.convertValues(source["tasks"], TaskDeleteReport);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class DenialView {
	    tool: string;
	    command?: string;
	    detail?: string;
	    write?: boolean;
	    grantable?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new DenialView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tool = source["tool"];
	        this.command = source["command"];
	        this.detail = source["detail"];
	        this.write = source["write"];
	        this.grantable = source["grantable"];
	    }
	}
	export class DocGraphView {
	    nodes: docs.GraphNode[];
	    edges: docs.GraphEdge[];
	    warnings?: string[];
	
	    static createFrom(source: any = {}) {
	        return new DocGraphView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.nodes = this.convertValues(source["nodes"], docs.GraphNode);
	        this.edges = this.convertValues(source["edges"], docs.GraphEdge);
	        this.warnings = source["warnings"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class DocIndexView {
	    path: string;
	    docs: docs.Doc[];
	
	    static createFrom(source: any = {}) {
	        return new DocIndexView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.docs = this.convertValues(source["docs"], docs.Doc);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class DocReadView {
	    taskId: string;
	    stepId: string;
	    runId: string;
	    projectId: string;
	    agent: string;
	    runtime: string;
	    docsDir: string;
	    docPath: string;
	    tool: string;
	    // Go type: time
	    openedAt: any;
	    delivered: boolean;
	
	    static createFrom(source: any = {}) {
	        return new DocReadView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.taskId = source["taskId"];
	        this.stepId = source["stepId"];
	        this.runId = source["runId"];
	        this.projectId = source["projectId"];
	        this.agent = source["agent"];
	        this.runtime = source["runtime"];
	        this.docsDir = source["docsDir"];
	        this.docPath = source["docPath"];
	        this.tool = source["tool"];
	        this.openedAt = this.convertValues(source["openedAt"], null);
	        this.delivered = source["delivered"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class DocsView {
	    path: string;
	    tree: docs.Folder;
	
	    static createFrom(source: any = {}) {
	        return new DocsView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.tree = this.convertValues(source["tree"], docs.Folder);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class FailedCheckView {
	    checkId: string;
	    argv: string[];
	    exitCode?: number;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new FailedCheckView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.checkId = source["checkId"];
	        this.argv = source["argv"];
	        this.exitCode = source["exitCode"];
	        this.error = source["error"];
	    }
	}
	export class FailedStepView {
	    stepId: string;
	    title: string;
	    state: string;
	    error?: string;
	    denials: DenialView[];
	    checks: FailedCheckView[];
	
	    static createFrom(source: any = {}) {
	        return new FailedStepView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.stepId = source["stepId"];
	        this.title = source["title"];
	        this.state = source["state"];
	        this.error = source["error"];
	        this.denials = this.convertValues(source["denials"], DenialView);
	        this.checks = this.convertValues(source["checks"], FailedCheckView);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class FeedbackRequestItem {
	    target: string;
	    text: string;
	
	    static createFrom(source: any = {}) {
	        return new FeedbackRequestItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.target = source["target"];
	        this.text = source["text"];
	    }
	}
	export class FeedbackRequest {
	    round: number;
	    items: FeedbackRequestItem[];
	
	    static createFrom(source: any = {}) {
	        return new FeedbackRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.round = source["round"];
	        this.items = this.convertValues(source["items"], FeedbackRequestItem);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class FeedbackRoundView {
	    round: number;
	    items: storage.FeedbackItem[];
	
	    static createFrom(source: any = {}) {
	        return new FeedbackRoundView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.round = source["round"];
	        this.items = this.convertValues(source["items"], storage.FeedbackItem);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class FlowLocation {
	    rule: number;
	    entry: number;
	
	    static createFrom(source: any = {}) {
	        return new FlowLocation(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.rule = source["rule"];
	        this.entry = source["entry"];
	    }
	}
	export class GateStep {
	    stepId: string;
	    title: string;
	    agent: string;
	    round: number;
	    artifacts: domain.ArtifactRef[];
	
	    static createFrom(source: any = {}) {
	        return new GateStep(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.stepId = source["stepId"];
	        this.title = source["title"];
	        this.agent = source["agent"];
	        this.round = source["round"];
	        this.artifacts = this.convertValues(source["artifacts"], domain.ArtifactRef);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class InputView {
	    id: string;
	    executionId: string;
	    stepId: string;
	    a2aTaskId?: string;
	    agent: string;
	    askedBy: string;
	    question: string;
	    options?: string[];
	    context?: string;
	    state: string;
	    answer?: string;
	    // Go type: time
	    createdAt: any;
	    // Go type: time
	    resolvedAt?: any;
	    review?: boolean;
	    reviewTarget?: string;
	    checkPause?: boolean;
	    stage?: boolean;
	    permissions?: storage.PermissionAsk[];
	    runTitle?: string;
	    expired?: boolean;
	    previousAnswer?: string;
	
	    static createFrom(source: any = {}) {
	        return new InputView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.executionId = source["executionId"];
	        this.stepId = source["stepId"];
	        this.a2aTaskId = source["a2aTaskId"];
	        this.agent = source["agent"];
	        this.askedBy = source["askedBy"];
	        this.question = source["question"];
	        this.options = source["options"];
	        this.context = source["context"];
	        this.state = source["state"];
	        this.answer = source["answer"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
	        this.resolvedAt = this.convertValues(source["resolvedAt"], null);
	        this.review = source["review"];
	        this.reviewTarget = source["reviewTarget"];
	        this.checkPause = source["checkPause"];
	        this.stage = source["stage"];
	        this.permissions = this.convertValues(source["permissions"], storage.PermissionAsk);
	        this.runTitle = source["runTitle"];
	        this.expired = source["expired"];
	        this.previousAnswer = source["previousAnswer"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class KeptWorktreeView {
	    path: string;
	    projectId: string;
	    projectName?: string;
	    branch?: string;
	    taskId: string;
	    runId: string;
	    title: string;
	    reason: string;
	    keptAt: string;
	    bytes: number;
	
	    static createFrom(source: any = {}) {
	        return new KeptWorktreeView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.projectId = source["projectId"];
	        this.projectName = source["projectName"];
	        this.branch = source["branch"];
	        this.taskId = source["taskId"];
	        this.runId = source["runId"];
	        this.title = source["title"];
	        this.reason = source["reason"];
	        this.keptAt = source["keptAt"];
	        this.bytes = source["bytes"];
	    }
	}
	export class LibraryDefinition {
	    name: string;
	    scope: string;
	    file?: string;
	    kind: string;
	    stage?: rules.Stage;
	    group?: rules.Group;
	    overridden?: boolean;
	    ambiguous?: boolean;
	    readOnly?: string;
	    usedBy?: string[];
	
	    static createFrom(source: any = {}) {
	        return new LibraryDefinition(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.scope = source["scope"];
	        this.file = source["file"];
	        this.kind = source["kind"];
	        this.stage = this.convertValues(source["stage"], rules.Stage);
	        this.group = this.convertValues(source["group"], rules.Group);
	        this.overridden = source["overridden"];
	        this.ambiguous = source["ambiguous"];
	        this.readOnly = source["readOnly"];
	        this.usedBy = source["usedBy"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class LinearIssueView {
	    id: string;
	    identifier: string;
	    title: string;
	    description: string;
	    url: string;
	    priority: number;
	    dueDate?: string;
	    team: string;
	    state: string;
	    project?: string;
	    parent?: string;
	    labels: string[];
	    suggestedKind: string;
	
	    static createFrom(source: any = {}) {
	        return new LinearIssueView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.identifier = source["identifier"];
	        this.title = source["title"];
	        this.description = source["description"];
	        this.url = source["url"];
	        this.priority = source["priority"];
	        this.dueDate = source["dueDate"];
	        this.team = source["team"];
	        this.state = source["state"];
	        this.project = source["project"];
	        this.parent = source["parent"];
	        this.labels = source["labels"];
	        this.suggestedKind = source["suggestedKind"];
	    }
	}
	export class PlanSuggestion {
	    id: string;
	    stepId: string;
	    text: string;
	    command?: string[];
	    permission?: string;
	
	    static createFrom(source: any = {}) {
	        return new PlanSuggestion(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.stepId = source["stepId"];
	        this.text = source["text"];
	        this.command = source["command"];
	        this.permission = source["permission"];
	    }
	}
	export class PlanTitleView {
	    task: domain.TaskSpec;
	    branching?: domain.BranchPlan;
	
	    static createFrom(source: any = {}) {
	        return new PlanTitleView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.task = this.convertValues(source["task"], domain.TaskSpec);
	        this.branching = this.convertValues(source["branching"], domain.BranchPlan);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class ProjectRemoval {
	    project: ProjectView;
	    deletedTasks: number;
	
	    static createFrom(source: any = {}) {
	        return new ProjectRemoval(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.project = this.convertValues(source["project"], ProjectView);
	        this.deletedTasks = source["deletedTasks"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ProjectRemovalPreview {
	    projectId: string;
	    name: string;
	    repository: string;
	    refused?: string;
	    tasks: number;
	    runs: number;
	    worktrees: string[];
	
	    static createFrom(source: any = {}) {
	        return new ProjectRemovalPreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.projectId = source["projectId"];
	        this.name = source["name"];
	        this.repository = source["repository"];
	        this.refused = source["refused"];
	        this.tasks = source["tasks"];
	        this.runs = source["runs"];
	        this.worktrees = source["worktrees"];
	    }
	}
	export class ProjectSyncView {
	    projectId: string;
	    summary: worktree.SyncSummary;
	    fetched?: boolean;
	    fetchError?: string;
	
	    static createFrom(source: any = {}) {
	        return new ProjectSyncView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.projectId = source["projectId"];
	        this.summary = this.convertValues(source["summary"], worktree.SyncSummary);
	        this.fetched = source["fetched"];
	        this.fetchError = source["fetchError"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	export class PullRequestCheck {
	    checked: number;
	    linked: number;
	    removed: string[];
	    problems?: string[];
	
	    static createFrom(source: any = {}) {
	        return new PullRequestCheck(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.checked = source["checked"];
	        this.linked = source["linked"];
	        this.removed = source["removed"];
	        this.problems = source["problems"];
	    }
	}
	export class PullRequestView {
	    number: number;
	    title: string;
	    url: string;
	    state: string;
	    mergedAt?: string;
	    checkedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new PullRequestView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.number = source["number"];
	        this.title = source["title"];
	        this.url = source["url"];
	        this.state = source["state"];
	        this.mergedAt = source["mergedAt"];
	        this.checkedAt = source["checkedAt"];
	    }
	}
	export class RemoteAgentPreview {
	    card: remote.Card;
	    pin: remote.Pin;
	    suggestedName: string;
	    samplePayload: string;
	    samplePayloadWithArtifacts: string;
	    changed: boolean;
	    changes: CardChange[];
	
	    static createFrom(source: any = {}) {
	        return new RemoteAgentPreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.card = this.convertValues(source["card"], remote.Card);
	        this.pin = this.convertValues(source["pin"], remote.Pin);
	        this.suggestedName = source["suggestedName"];
	        this.samplePayload = source["samplePayload"];
	        this.samplePayloadWithArtifacts = source["samplePayloadWithArtifacts"];
	        this.changed = source["changed"];
	        this.changes = this.convertValues(source["changes"], CardChange);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RemoteAgentView {
	    name: string;
	    cardUrl: string;
	    enabled: boolean;
	    secret?: string;
	    auto?: boolean;
	    sendArtifacts?: boolean;
	    maxArtifactBytes?: number;
	    allowInsecure?: boolean;
	    description?: string;
	    skills?: string[];
	    pinned?: remote.Pin;
	    secretStatus?: secrets.Status;
	    shadowed?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new RemoteAgentView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.cardUrl = source["cardUrl"];
	        this.enabled = source["enabled"];
	        this.secret = source["secret"];
	        this.auto = source["auto"];
	        this.sendArtifacts = source["sendArtifacts"];
	        this.maxArtifactBytes = source["maxArtifactBytes"];
	        this.allowInsecure = source["allowInsecure"];
	        this.description = source["description"];
	        this.skills = source["skills"];
	        this.pinned = this.convertValues(source["pinned"], remote.Pin);
	        this.secretStatus = this.convertValues(source["secretStatus"], secrets.Status);
	        this.shadowed = source["shadowed"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RepositoryCandidate {
	    path: string;
	    name: string;
	    refused?: string;
	    restores?: string;
	
	    static createFrom(source: any = {}) {
	        return new RepositoryCandidate(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.name = source["name"];
	        this.refused = source["refused"];
	        this.restores = source["restores"];
	    }
	}
	
	export class RerunRequest {
	    round: number;
	    workflow: string;
	    startOver: boolean;
	    discard: boolean;
	    changes: string[];
	
	    static createFrom(source: any = {}) {
	        return new RerunRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.round = source["round"];
	        this.workflow = source["workflow"];
	        this.startOver = source["startOver"];
	        this.discard = source["discard"];
	        this.changes = source["changes"];
	    }
	}
	export class ResumeStep {
	    id: string;
	    title: string;
	    state: string;
	
	    static createFrom(source: any = {}) {
	        return new ResumeStep(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.state = source["state"];
	    }
	}
	export class ResumeView {
	    round: number;
	    stopped: boolean;
	    refusal?: string;
	    rerun: ResumeStep[];
	    kept: ResumeStep[];
	    workspace?: string;
	    changes: worktree.PendingChange[];
	
	    static createFrom(source: any = {}) {
	        return new ResumeView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.round = source["round"];
	        this.stopped = source["stopped"];
	        this.refusal = source["refusal"];
	        this.rerun = this.convertValues(source["rerun"], ResumeStep);
	        this.kept = this.convertValues(source["kept"], ResumeStep);
	        this.workspace = source["workspace"];
	        this.changes = this.convertValues(source["changes"], worktree.PendingChange);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RerunRevision {
	    workflow: domain.WorkflowSpec;
	    edited: string[];
	    resume: ResumeView;
	
	    static createFrom(source: any = {}) {
	        return new RerunRevision(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.workflow = this.convertValues(source["workflow"], domain.WorkflowSpec);
	        this.edited = source["edited"];
	        this.resume = this.convertValues(source["resume"], ResumeView);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class WorkflowPlanView {
	    project: ProjectView;
	    task: domain.TaskSpec;
	    workflow: domain.WorkflowSpec;
	    ruleName: string;
	    orchestrated: boolean;
	    decision: orchestrator.Decision;
	    notes?: string[];
	    events?: domain.RunEvent[];
	    candidates?: AgentCandidateView[];
	    usage: domain.UsageRecord[];
	    staffingReused?: string;
	    mcpServers?: domain.MCPServer[];
	
	    static createFrom(source: any = {}) {
	        return new WorkflowPlanView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.project = this.convertValues(source["project"], ProjectView);
	        this.task = this.convertValues(source["task"], domain.TaskSpec);
	        this.workflow = this.convertValues(source["workflow"], domain.WorkflowSpec);
	        this.ruleName = source["ruleName"];
	        this.orchestrated = source["orchestrated"];
	        this.decision = this.convertValues(source["decision"], orchestrator.Decision);
	        this.notes = source["notes"];
	        this.events = this.convertValues(source["events"], domain.RunEvent);
	        this.candidates = this.convertValues(source["candidates"], AgentCandidateView);
	        this.usage = this.convertValues(source["usage"], domain.UsageRecord);
	        this.staffingReused = source["staffingReused"];
	        this.mcpServers = this.convertValues(source["mcpServers"], domain.MCPServer);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RerunView {
	    runId: string;
	    plan: WorkflowPlanView;
	    resume: ResumeView;
	
	    static createFrom(source: any = {}) {
	        return new RerunView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.runId = source["runId"];
	        this.plan = this.convertValues(source["plan"], WorkflowPlanView);
	        this.resume = this.convertValues(source["resume"], ResumeView);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ResumeRequest {
	    round: number;
	    discard: boolean;
	    changes: string[];
	
	    static createFrom(source: any = {}) {
	        return new ResumeRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.round = source["round"];
	        this.discard = source["discard"];
	        this.changes = source["changes"];
	    }
	}
	
	
	
	export class TrackerChangeView {
	    id: string;
	    round: number;
	    stepId: string;
	    tracker?: string;
	    kind: string;
	    issue?: string;
	    summary: string;
	    change: domain.TrackerChange;
	    state: string;
	    reason?: string;
	    url?: string;
	    retry?: boolean;
	    updatedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new TrackerChangeView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.round = source["round"];
	        this.stepId = source["stepId"];
	        this.tracker = source["tracker"];
	        this.kind = source["kind"];
	        this.issue = source["issue"];
	        this.summary = source["summary"];
	        this.change = this.convertValues(source["change"], domain.TrackerChange);
	        this.state = source["state"];
	        this.reason = source["reason"];
	        this.url = source["url"];
	        this.retry = source["retry"];
	        this.updatedAt = source["updatedAt"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ReviewView {
	    round: number;
	    approve: boolean;
	    targets: ReviewTarget[];
	    target?: string;
	    refusal?: string;
	    drafts: number;
	    trackerChanges: TrackerChangeView[];
	
	    static createFrom(source: any = {}) {
	        return new ReviewView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.round = source["round"];
	        this.approve = source["approve"];
	        this.targets = this.convertValues(source["targets"], ReviewTarget);
	        this.target = source["target"];
	        this.refusal = source["refusal"];
	        this.drafts = source["drafts"];
	        this.trackerChanges = this.convertValues(source["trackerChanges"], TrackerChangeView);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class StepRunView {
	    id: string;
	    title: string;
	    kind: string;
	    agent?: string;
	    runtime?: string;
	    model?: string;
	    state: string;
	    a2aTaskId?: string;
	    summary?: string;
	    startedAt?: string;
	    finishedAt?: string;
	    round?: number;
	    from?: domain.StepOrigin;
	
	    static createFrom(source: any = {}) {
	        return new StepRunView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.kind = source["kind"];
	        this.agent = source["agent"];
	        this.runtime = source["runtime"];
	        this.model = source["model"];
	        this.state = source["state"];
	        this.a2aTaskId = source["a2aTaskId"];
	        this.summary = source["summary"];
	        this.startedAt = source["startedAt"];
	        this.finishedAt = source["finishedAt"];
	        this.round = source["round"];
	        this.from = this.convertValues(source["from"], domain.StepOrigin);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RoundView {
	    round: number;
	    since?: string;
	    startedAt?: string;
	    finishedAt?: string;
	    steps: StepRunView[];
	    events: domain.RunEvent[];
	    hiddenEvents?: number;
	    feedback: storage.FeedbackItem[];
	    resumed?: boolean;
	    discarded?: boolean;
	    edited?: string[];
	
	    static createFrom(source: any = {}) {
	        return new RoundView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.round = source["round"];
	        this.since = source["since"];
	        this.startedAt = source["startedAt"];
	        this.finishedAt = source["finishedAt"];
	        this.steps = this.convertValues(source["steps"], StepRunView);
	        this.events = this.convertValues(source["events"], domain.RunEvent);
	        this.hiddenEvents = source["hiddenEvents"];
	        this.feedback = this.convertValues(source["feedback"], storage.FeedbackItem);
	        this.resumed = source["resumed"];
	        this.discarded = source["discarded"];
	        this.edited = source["edited"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RuleEditView {
	    stepId: string;
	    field: string;
	    text: string;
	
	    static createFrom(source: any = {}) {
	        return new RuleEditView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.stepId = source["stepId"];
	        this.field = source["field"];
	        this.text = source["text"];
	    }
	}
	export class RulesView {
	    document: rules.Document;
	    exists: boolean;
	    path: string;
	
	    static createFrom(source: any = {}) {
	        return new RulesView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.document = this.convertValues(source["document"], rules.Document);
	        this.exists = source["exists"];
	        this.path = source["path"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RuleEditsView {
	    projectId: string;
	    ruleName: string;
	    ruleIndex?: number;
	    rules: RulesView;
	    changes: RuleEditView[];
	    leftOut: RuleEditView[];
	
	    static createFrom(source: any = {}) {
	        return new RuleEditsView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.projectId = source["projectId"];
	        this.ruleName = source["ruleName"];
	        this.ruleIndex = source["ruleIndex"];
	        this.rules = this.convertValues(source["rules"], RulesView);
	        this.changes = this.convertValues(source["changes"], RuleEditView);
	        this.leftOut = this.convertValues(source["leftOut"], RuleEditView);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RulesProblem {
	    message: string;
	    flow?: FlowLocation;
	    definition?: string;
	
	    static createFrom(source: any = {}) {
	        return new RulesProblem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.message = source["message"];
	        this.flow = this.convertValues(source["flow"], FlowLocation);
	        this.definition = source["definition"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class WorktreePreview {
	    path: string;
	    bytes: number;
	    removable: boolean;
	    reason?: string;
	
	    static createFrom(source: any = {}) {
	        return new WorktreePreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.bytes = source["bytes"];
	        this.removable = source["removable"];
	        this.reason = source["reason"];
	    }
	}
	export class RunDeletePreview {
	    runId: string;
	    title: string;
	    state: string;
	    finishedAt?: string;
	    worktree?: WorktreePreview;
	    branch?: BranchPreview;
	
	    static createFrom(source: any = {}) {
	        return new RunDeletePreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.runId = source["runId"];
	        this.title = source["title"];
	        this.state = source["state"];
	        this.finishedAt = source["finishedAt"];
	        this.worktree = this.convertValues(source["worktree"], WorktreePreview);
	        this.branch = this.convertValues(source["branch"], BranchPreview);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RunFailureView {
	    round: number;
	    stopped: boolean;
	    error?: string;
	    steps: FailedStepView[];
	    suggestions: PlanSuggestion[];
	
	    static createFrom(source: any = {}) {
	        return new RunFailureView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.round = source["round"];
	        this.stopped = source["stopped"];
	        this.error = source["error"];
	        this.steps = this.convertValues(source["steps"], FailedStepView);
	        this.suggestions = this.convertValues(source["suggestions"], PlanSuggestion);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RunStoryView {
	    tracker: string;
	    id: string;
	    markDone?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new RunStoryView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tracker = source["tracker"];
	        this.id = source["id"];
	        this.markDone = source["markDone"];
	    }
	}
	export class RunTaskView {
	    task: a2a.Task;
	    feedback?: storage.FeedbackItem[];
	
	    static createFrom(source: any = {}) {
	        return new RunTaskView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.task = this.convertValues(source["task"], a2a.Task);
	        this.feedback = this.convertValues(source["feedback"], storage.FeedbackItem);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RunUsageView {
	    calls: domain.UsageRecord[];
	    total: domain.Usage;
	    cost: domain.CostTotal;
	
	    static createFrom(source: any = {}) {
	        return new RunUsageView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.calls = this.convertValues(source["calls"], domain.UsageRecord);
	        this.total = this.convertValues(source["total"], domain.Usage);
	        this.cost = this.convertValues(source["cost"], domain.CostTotal);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RuntimeTestView {
	    available: boolean;
	    path?: string;
	    version?: string;
	    detail?: string;
	
	    static createFrom(source: any = {}) {
	        return new RuntimeTestView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.available = source["available"];
	        this.path = source["path"];
	        this.version = source["version"];
	        this.detail = source["detail"];
	    }
	}
	export class RuntimeView {
	    name: string;
	    label: string;
	    builtIn: boolean;
	    customized?: boolean;
	    preset?: string;
	    binary: string;
	    canWrite: boolean;
	    config: config.RuntimeConfig;
	    override?: string;
	
	    static createFrom(source: any = {}) {
	        return new RuntimeView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.label = source["label"];
	        this.builtIn = source["builtIn"];
	        this.customized = source["customized"];
	        this.preset = source["preset"];
	        this.binary = source["binary"];
	        this.canWrite = source["canWrite"];
	        this.config = this.convertValues(source["config"], config.RuntimeConfig);
	        this.override = source["override"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	export class SkillView {
	    id: string;
	    name: string;
	    description: string;
	    taskKinds: string[];
	    requiredCapabilities: string[];
	
	    static createFrom(source: any = {}) {
	        return new SkillView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.taskKinds = source["taskKinds"];
	        this.requiredCapabilities = source["requiredCapabilities"];
	    }
	}
	export class StageLibraryView {
	    definitions: LibraryDefinition[];
	    problems: rules.LibraryProblem[];
	    overridable: rules.OverridableField[];
	
	    static createFrom(source: any = {}) {
	        return new StageLibraryView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.definitions = this.convertValues(source["definitions"], LibraryDefinition);
	        this.problems = this.convertValues(source["problems"], rules.LibraryProblem);
	        this.overridable = this.convertValues(source["overridable"], rules.OverridableField);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class StepContextView {
	    stepId: string;
	    agent: string;
	    envelope: domain.ContextEnvelope;
	    handoff?: domain.HandoffResult;
	    prompt: string;
	    docReads: DocReadView[];
	    usage: domain.UsageRecord[];
	    feedback: FeedbackRoundView[];
	
	    static createFrom(source: any = {}) {
	        return new StepContextView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.stepId = source["stepId"];
	        this.agent = source["agent"];
	        this.envelope = this.convertValues(source["envelope"], domain.ContextEnvelope);
	        this.handoff = this.convertValues(source["handoff"], domain.HandoffResult);
	        this.prompt = source["prompt"];
	        this.docReads = this.convertValues(source["docReads"], DocReadView);
	        this.usage = this.convertValues(source["usage"], domain.UsageRecord);
	        this.feedback = this.convertValues(source["feedback"], FeedbackRoundView);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class StorageUsage {
	    artifacts: number;
	    artifactBytes: number;
	    worktrees: number;
	    worktreeBytes: number;
	    kept: KeptWorktreeView[];
	    problems?: string[];
	
	    static createFrom(source: any = {}) {
	        return new StorageUsage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.artifacts = source["artifacts"];
	        this.artifactBytes = source["artifactBytes"];
	        this.worktrees = source["worktrees"];
	        this.worktreeBytes = source["worktreeBytes"];
	        this.kept = this.convertValues(source["kept"], KeptWorktreeView);
	        this.problems = source["problems"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class StoryDoneView {
	    pullRequest?: github.PullRequest;
	    merged: boolean;
	    summary: string;
	    notes: string[];
	
	    static createFrom(source: any = {}) {
	        return new StoryDoneView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.pullRequest = this.convertValues(source["pullRequest"], github.PullRequest);
	        this.merged = source["merged"];
	        this.summary = source["summary"];
	        this.notes = source["notes"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class TaskDeletePreview {
	    taskId: string;
	    projectId: string;
	    title: string;
	    refused?: string;
	    runs: RunDeletePreview[];
	
	    static createFrom(source: any = {}) {
	        return new TaskDeletePreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.taskId = source["taskId"];
	        this.projectId = source["projectId"];
	        this.title = source["title"];
	        this.refused = source["refused"];
	        this.runs = this.convertValues(source["runs"], RunDeletePreview);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class ThreadDetailView {
	    taskId: string;
	    contextId: string;
	    agent: string;
	    state: string;
	    messageCount: number;
	    lastMessage: a2a.Message;
	    // Go type: time
	    updatedAt: any;
	    runId?: string;
	    runTitle?: string;
	    preview?: string;
	    pendingInput?: InputView;
	    stepId?: string;
	    messages: a2a.Message[];
	    artifacts: a2a.Artifact[];
	    inputs: InputView[];
	    feedback?: storage.FeedbackItem[];
	
	    static createFrom(source: any = {}) {
	        return new ThreadDetailView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.taskId = source["taskId"];
	        this.contextId = source["contextId"];
	        this.agent = source["agent"];
	        this.state = source["state"];
	        this.messageCount = source["messageCount"];
	        this.lastMessage = this.convertValues(source["lastMessage"], a2a.Message);
	        this.updatedAt = this.convertValues(source["updatedAt"], null);
	        this.runId = source["runId"];
	        this.runTitle = source["runTitle"];
	        this.preview = source["preview"];
	        this.pendingInput = this.convertValues(source["pendingInput"], InputView);
	        this.stepId = source["stepId"];
	        this.messages = this.convertValues(source["messages"], a2a.Message);
	        this.artifacts = this.convertValues(source["artifacts"], a2a.Artifact);
	        this.inputs = this.convertValues(source["inputs"], InputView);
	        this.feedback = this.convertValues(source["feedback"], storage.FeedbackItem);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ThreadView {
	    taskId: string;
	    contextId: string;
	    agent: string;
	    state: string;
	    messageCount: number;
	    lastMessage: a2a.Message;
	    // Go type: time
	    updatedAt: any;
	    runId?: string;
	    runTitle?: string;
	    preview?: string;
	    pendingInput?: InputView;
	
	    static createFrom(source: any = {}) {
	        return new ThreadView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.taskId = source["taskId"];
	        this.contextId = source["contextId"];
	        this.agent = source["agent"];
	        this.state = source["state"];
	        this.messageCount = source["messageCount"];
	        this.lastMessage = this.convertValues(source["lastMessage"], a2a.Message);
	        this.updatedAt = this.convertValues(source["updatedAt"], null);
	        this.runId = source["runId"];
	        this.runTitle = source["runTitle"];
	        this.preview = source["preview"];
	        this.pendingInput = this.convertValues(source["pendingInput"], InputView);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	export class WorkflowRequestView {
	    projectId: string;
	    title?: string;
	    objective: string;
	    acceptanceCriteria: string[];
	    constraints: string[];
	    labels?: string[];
	    kind: string;
	    sourceKind: string;
	    sourceReference?: string;
	    ruleName?: string;
	    restaff?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new WorkflowRequestView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.projectId = source["projectId"];
	        this.title = source["title"];
	        this.objective = source["objective"];
	        this.acceptanceCriteria = source["acceptanceCriteria"];
	        this.constraints = source["constraints"];
	        this.labels = source["labels"];
	        this.kind = source["kind"];
	        this.sourceKind = source["sourceKind"];
	        this.sourceReference = source["sourceReference"];
	        this.ruleName = source["ruleName"];
	        this.restaff = source["restaff"];
	    }
	}
	export class WorkflowRunView {
	    id: string;
	    workflowId: string;
	    taskId: string;
	    projectId: string;
	    state: string;
	    title: string;
	    runtime?: string;
	    model?: string;
	    agent?: string;
	    ruleName?: string;
	    orchestrated: boolean;
	    worktree?: string;
	    branch?: string;
	    baseCommit?: string;
	    cleanup?: string;
	    worktreeRemovedAt?: string;
	    workspaceNotes?: string[];
	    startedAt: string;
	    finishedAt?: string;
	    error?: string;
	    steps: StepRunView[];
	    lastCommit?: CommitView;
	    pendingInputs: number;
	    live: boolean;
	    events: domain.RunEvent[];
	    taskCost?: domain.CallTotals;
	    checks?: CheckTotals;
	    round: number;
	    story?: RunStoryView;
	    trackerChanges?: TrackerChangeView[];
	    deleteRefused?: string;
	    pullRequest?: PullRequestView;
	    rerunOf?: string;
	
	    static createFrom(source: any = {}) {
	        return new WorkflowRunView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.workflowId = source["workflowId"];
	        this.taskId = source["taskId"];
	        this.projectId = source["projectId"];
	        this.state = source["state"];
	        this.title = source["title"];
	        this.runtime = source["runtime"];
	        this.model = source["model"];
	        this.agent = source["agent"];
	        this.ruleName = source["ruleName"];
	        this.orchestrated = source["orchestrated"];
	        this.worktree = source["worktree"];
	        this.branch = source["branch"];
	        this.baseCommit = source["baseCommit"];
	        this.cleanup = source["cleanup"];
	        this.worktreeRemovedAt = source["worktreeRemovedAt"];
	        this.workspaceNotes = source["workspaceNotes"];
	        this.startedAt = source["startedAt"];
	        this.finishedAt = source["finishedAt"];
	        this.error = source["error"];
	        this.steps = this.convertValues(source["steps"], StepRunView);
	        this.lastCommit = this.convertValues(source["lastCommit"], CommitView);
	        this.pendingInputs = source["pendingInputs"];
	        this.live = source["live"];
	        this.events = this.convertValues(source["events"], domain.RunEvent);
	        this.taskCost = this.convertValues(source["taskCost"], domain.CallTotals);
	        this.checks = this.convertValues(source["checks"], CheckTotals);
	        this.round = source["round"];
	        this.story = this.convertValues(source["story"], RunStoryView);
	        this.trackerChanges = this.convertValues(source["trackerChanges"], TrackerChangeView);
	        this.deleteRefused = source["deleteRefused"];
	        this.pullRequest = this.convertValues(source["pullRequest"], PullRequestView);
	        this.rerunOf = source["rerunOf"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	

}

export namespace catalog {
	
	export class Added {
	    kind: string;
	    name: string;
	    path: string;
	
	    static createFrom(source: any = {}) {
	        return new Added(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.name = source["name"];
	        this.path = source["path"];
	    }
	}
	export class Needs {
	    agents?: string[];
	    stages?: string[];
	    skills?: string[];
	
	    static createFrom(source: any = {}) {
	        return new Needs(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.agents = source["agents"];
	        this.stages = source["stages"];
	        this.skills = source["skills"];
	    }
	}
	export class Item {
	    name: string;
	    kind: string;
	    description: string;
	    needs: Needs;
	    permission?: string;
	    taskKinds?: string[];
	    path: string;
	    content: string;
	    files?: string[];
	    source?: string;
	    scope?: string;
	    version?: string;
	
	    static createFrom(source: any = {}) {
	        return new Item(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.kind = source["kind"];
	        this.description = source["description"];
	        this.needs = this.convertValues(source["needs"], Needs);
	        this.permission = source["permission"];
	        this.taskKinds = source["taskKinds"];
	        this.path = source["path"];
	        this.content = source["content"];
	        this.files = source["files"];
	        this.source = source["source"];
	        this.scope = source["scope"];
	        this.version = source["version"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class Result {
	    item: string;
	    added: Added[];
	    notes?: string[];
	
	    static createFrom(source: any = {}) {
	        return new Result(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.item = source["item"];
	        this.added = this.convertValues(source["added"], Added);
	        this.notes = source["notes"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace config {
	
	export class AgentsConfig {
	    directories?: string[];
	
	    static createFrom(source: any = {}) {
	        return new AgentsConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.directories = source["directories"];
	    }
	}
	export class ChecksConfig {
	    logBytes?: number;
	
	    static createFrom(source: any = {}) {
	        return new ChecksConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.logBytes = source["logBytes"];
	    }
	}
	export class ConcurrencyConfig {
	    maxTasks?: number;
	    maxWorkers?: number;
	
	    static createFrom(source: any = {}) {
	        return new ConcurrencyConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.maxTasks = source["maxTasks"];
	        this.maxWorkers = source["maxWorkers"];
	    }
	}
	export class FieldError {
	    field: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new FieldError(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.field = source["field"];
	        this.message = source["message"];
	    }
	}
	export class MCPServerConfig {
	    name: string;
	    description?: string;
	    command?: string[];
	    url?: string;
	    env?: Record<string, string>;
	    bearer?: string;
	    tools?: string[];
	    tracker?: string;
	
	    static createFrom(source: any = {}) {
	        return new MCPServerConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.command = source["command"];
	        this.url = source["url"];
	        this.env = source["env"];
	        this.bearer = source["bearer"];
	        this.tools = source["tools"];
	        this.tracker = source["tracker"];
	    }
	}
	export class RemoteAgentConfig {
	    name: string;
	    cardUrl: string;
	    enabled: boolean;
	    secret?: string;
	    auto?: boolean;
	    sendArtifacts?: boolean;
	    maxArtifactBytes?: number;
	    allowInsecure?: boolean;
	    description?: string;
	    skills?: string[];
	    pinned?: remote.Pin;
	
	    static createFrom(source: any = {}) {
	        return new RemoteAgentConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.cardUrl = source["cardUrl"];
	        this.enabled = source["enabled"];
	        this.secret = source["secret"];
	        this.auto = source["auto"];
	        this.sendArtifacts = source["sendArtifacts"];
	        this.maxArtifactBytes = source["maxArtifactBytes"];
	        this.allowInsecure = source["allowInsecure"];
	        this.description = source["description"];
	        this.skills = source["skills"];
	        this.pinned = this.convertValues(source["pinned"], remote.Pin);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RoutingConfig {
	    bands?: Record<string, Array<routing.Band>>;
	
	    static createFrom(source: any = {}) {
	        return new RoutingConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.bands = this.convertValues(source["bands"], Array<routing.Band>, true);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class GitHubConfig {
	    host?: string;
	    protocol?: string;
	    binary?: string;
	
	    static createFrom(source: any = {}) {
	        return new GitHubConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.host = source["host"];
	        this.protocol = source["protocol"];
	        this.binary = source["binary"];
	    }
	}
	export class LinearConfig {
	    endpoint?: string;
	
	    static createFrom(source: any = {}) {
	        return new LinearConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.endpoint = source["endpoint"];
	    }
	}
	export class StagesConfig {
	    directories?: string[];
	
	    static createFrom(source: any = {}) {
	        return new StagesConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.directories = source["directories"];
	    }
	}
	export class TrackerUpdatesOverride {
	    start?: boolean;
	    result?: boolean;
	    done?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new TrackerUpdatesOverride(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.start = source["start"];
	        this.result = source["result"];
	        this.done = source["done"];
	    }
	}
	export class TrackerOverride {
	    writes?: string[];
	    updates?: TrackerUpdatesOverride;
	
	    static createFrom(source: any = {}) {
	        return new TrackerOverride(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.writes = source["writes"];
	        this.updates = this.convertValues(source["updates"], TrackerUpdatesOverride);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RepositoryConfig {
	    name: string;
	    path: string;
	    defaultRuntime: string;
	    rules?: string;
	    docs?: string;
	    tracker?: TrackerOverride;
	
	    static createFrom(source: any = {}) {
	        return new RepositoryConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.defaultRuntime = source["defaultRuntime"];
	        this.rules = source["rules"];
	        this.docs = source["docs"];
	        this.tracker = this.convertValues(source["tracker"], TrackerOverride);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SecretRef {
	    name: string;
	    description?: string;
	    required?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SecretRef(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.required = source["required"];
	    }
	}
	export class NotificationsConfig {
	    desktop: boolean;
	
	    static createFrom(source: any = {}) {
	        return new NotificationsConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.desktop = source["desktop"];
	    }
	}
	export class TimeoutsConfig {
	    planning?: string;
	    worker?: string;
	    verifier?: string;
	    approval?: string;
	    health?: string;
	    auth?: string;
	    linear?: string;
	
	    static createFrom(source: any = {}) {
	        return new TimeoutsConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.planning = source["planning"];
	        this.worker = source["worker"];
	        this.verifier = source["verifier"];
	        this.approval = source["approval"];
	        this.health = source["health"];
	        this.auth = source["auth"];
	        this.linear = source["linear"];
	    }
	}
	export class RuntimeConfig {
	    label?: string;
	    preset?: string;
	    binary?: string;
	    args?: string[];
	    readOnlyArgs?: string[];
	    writeArgs?: string[];
	    stdin?: string;
	    output?: string;
	    versionArgs?: string[];
	    secrets?: string[];
	    userConfig?: boolean;
	    userMcp?: string[];
	
	    static createFrom(source: any = {}) {
	        return new RuntimeConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.label = source["label"];
	        this.preset = source["preset"];
	        this.binary = source["binary"];
	        this.args = source["args"];
	        this.readOnlyArgs = source["readOnlyArgs"];
	        this.writeArgs = source["writeArgs"];
	        this.stdin = source["stdin"];
	        this.output = source["output"];
	        this.versionArgs = source["versionArgs"];
	        this.secrets = source["secrets"];
	        this.userConfig = source["userConfig"];
	        this.userMcp = source["userMcp"];
	    }
	}
	export class OrchestratorConfig {
	    runtime: string;
	    model?: string;
	    reasoningEffort?: string;
	    maxTurns?: number;
	    readTools?: boolean;
	    disabled?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new OrchestratorConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.runtime = source["runtime"];
	        this.model = source["model"];
	        this.reasoningEffort = source["reasoningEffort"];
	        this.maxTurns = source["maxTurns"];
	        this.readTools = source["readTools"];
	        this.disabled = source["disabled"];
	    }
	}
	export class File {
	    version: number;
	    dataDir?: string;
	    orchestrator: OrchestratorConfig;
	    runtimes: Record<string, RuntimeConfig>;
	    concurrency: ConcurrencyConfig;
	    timeouts: TimeoutsConfig;
	    notifications: NotificationsConfig;
	    secrets: SecretRef[];
	    repositories: RepositoryConfig[];
	    agents: AgentsConfig;
	    stages: StagesConfig;
	    linear: LinearConfig;
	    github: GitHubConfig;
	    routing: RoutingConfig;
	    remoteAgents?: RemoteAgentConfig[];
	    mcpServers?: MCPServerConfig[];
	    budgets: domain.Budgets;
	    pricing: pricing.Overrides;
	    checks: ChecksConfig;
	
	    static createFrom(source: any = {}) {
	        return new File(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.dataDir = source["dataDir"];
	        this.orchestrator = this.convertValues(source["orchestrator"], OrchestratorConfig);
	        this.runtimes = this.convertValues(source["runtimes"], RuntimeConfig, true);
	        this.concurrency = this.convertValues(source["concurrency"], ConcurrencyConfig);
	        this.timeouts = this.convertValues(source["timeouts"], TimeoutsConfig);
	        this.notifications = this.convertValues(source["notifications"], NotificationsConfig);
	        this.secrets = this.convertValues(source["secrets"], SecretRef);
	        this.repositories = this.convertValues(source["repositories"], RepositoryConfig);
	        this.agents = this.convertValues(source["agents"], AgentsConfig);
	        this.stages = this.convertValues(source["stages"], StagesConfig);
	        this.linear = this.convertValues(source["linear"], LinearConfig);
	        this.github = this.convertValues(source["github"], GitHubConfig);
	        this.routing = this.convertValues(source["routing"], RoutingConfig);
	        this.remoteAgents = this.convertValues(source["remoteAgents"], RemoteAgentConfig);
	        this.mcpServers = this.convertValues(source["mcpServers"], MCPServerConfig);
	        this.budgets = this.convertValues(source["budgets"], domain.Budgets);
	        this.pricing = this.convertValues(source["pricing"], pricing.Overrides);
	        this.checks = this.convertValues(source["checks"], ChecksConfig);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	
	
	
	export class Override {
	    field: string;
	    variable: string;
	    value: string;
	
	    static createFrom(source: any = {}) {
	        return new Override(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.field = source["field"];
	        this.variable = source["variable"];
	        this.value = source["value"];
	    }
	}
	
	
	
	
	
	
	
	

}

export namespace diff {
	
	export class File {
	    path: string;
	    oldPath?: string;
	    status: string;
	    oldMode?: string;
	    newMode?: string;
	    similarity?: number;
	    binary?: boolean;
	    additions: number;
	    deletions: number;
	    generated?: boolean;
	    blob?: string;
	
	    static createFrom(source: any = {}) {
	        return new File(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.oldPath = source["oldPath"];
	        this.status = source["status"];
	        this.oldMode = source["oldMode"];
	        this.newMode = source["newMode"];
	        this.similarity = source["similarity"];
	        this.binary = source["binary"];
	        this.additions = source["additions"];
	        this.deletions = source["deletions"];
	        this.generated = source["generated"];
	        this.blob = source["blob"];
	    }
	}
	export class Change {
	    from: string;
	    to: string;
	    files: File[];
	    additions: number;
	    deletions: number;
	
	    static createFrom(source: any = {}) {
	        return new Change(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.from = source["from"];
	        this.to = source["to"];
	        this.files = this.convertValues(source["files"], File);
	        this.additions = source["additions"];
	        this.deletions = source["deletions"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class Span {
	    text: string;
	    class?: string;
	    changed?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Span(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.text = source["text"];
	        this.class = source["class"];
	        this.changed = source["changed"];
	    }
	}
	export class Line {
	    kind: string;
	    old?: number;
	    new?: number;
	    text: string;
	    crlf?: boolean;
	    noNewline?: boolean;
	    spans?: Span[];
	
	    static createFrom(source: any = {}) {
	        return new Line(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.old = source["old"];
	        this.new = source["new"];
	        this.text = source["text"];
	        this.crlf = source["crlf"];
	        this.noNewline = source["noNewline"];
	        this.spans = this.convertValues(source["spans"], Span);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class FileExcerpt {
	    lines: Line[];
	    newTotal: number;
	
	    static createFrom(source: any = {}) {
	        return new FileExcerpt(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.lines = this.convertValues(source["lines"], Line);
	        this.newTotal = source["newTotal"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Hunk {
	    header: string;
	    section?: string;
	    oldStart: number;
	    oldLines: number;
	    newStart: number;
	    newLines: number;
	    lines: Line[];
	
	    static createFrom(source: any = {}) {
	        return new Hunk(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.header = source["header"];
	        this.section = source["section"];
	        this.oldStart = source["oldStart"];
	        this.oldLines = source["oldLines"];
	        this.newStart = source["newStart"];
	        this.newLines = source["newLines"];
	        this.lines = this.convertValues(source["lines"], Line);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class FilePatch {
	    file: File;
	    from: string;
	    to: string;
	    language?: string;
	    hunks: Hunk[];
	    bytes: number;
	    lines: number;
	    newTotal?: number;
	    tooLarge?: boolean;
	    loadable?: boolean;
	    reason?: string;
	
	    static createFrom(source: any = {}) {
	        return new FilePatch(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.file = this.convertValues(source["file"], File);
	        this.from = source["from"];
	        this.to = source["to"];
	        this.language = source["language"];
	        this.hunks = this.convertValues(source["hunks"], Hunk);
	        this.bytes = source["bytes"];
	        this.lines = source["lines"];
	        this.newTotal = source["newTotal"];
	        this.tooLarge = source["tooLarge"];
	        this.loadable = source["loadable"];
	        this.reason = source["reason"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	export class Scope {
	    kind: string;
	    commit?: string;
	    from?: string;
	    to?: string;
	
	    static createFrom(source: any = {}) {
	        return new Scope(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.commit = source["commit"];
	        this.from = source["from"];
	        this.to = source["to"];
	    }
	}

}

export namespace docs {
	
	export class Link {
	    kind: string;
	    path?: string;
	    url?: string;
	
	    static createFrom(source: any = {}) {
	        return new Link(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.path = source["path"];
	        this.url = source["url"];
	    }
	}
	export class Doc {
	    path: string;
	    title: string;
	    lastUpdated: string;
	    author: string;
	    tags: string[];
	    status: string;
	    problems?: string[];
	    links?: Link[];
	
	    static createFrom(source: any = {}) {
	        return new Doc(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.title = source["title"];
	        this.lastUpdated = source["lastUpdated"];
	        this.author = source["author"];
	        this.tags = source["tags"];
	        this.status = source["status"];
	        this.problems = source["problems"];
	        this.links = this.convertValues(source["links"], Link);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Content {
	    doc: Doc;
	    markdown: string;
	
	    static createFrom(source: any = {}) {
	        return new Content(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.doc = this.convertValues(source["doc"], Doc);
	        this.markdown = source["markdown"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class Folder {
	    name: string;
	    path: string;
	    folders: Folder[];
	    docs: Doc[];
	
	    static createFrom(source: any = {}) {
	        return new Folder(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.folders = this.convertValues(source["folders"], Folder);
	        this.docs = this.convertValues(source["docs"], Doc);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class GraphEdge {
	    from: string;
	    to: string;
	    count: number;
	
	    static createFrom(source: any = {}) {
	        return new GraphEdge(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.from = source["from"];
	        this.to = source["to"];
	        this.count = source["count"];
	    }
	}
	export class GraphNode {
	    id: string;
	    kind: string;
	    label: string;
	    project?: string;
	    path?: string;
	    status?: string;
	    problems?: number;
	    url?: string;
	
	    static createFrom(source: any = {}) {
	        return new GraphNode(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.kind = source["kind"];
	        this.label = source["label"];
	        this.project = source["project"];
	        this.path = source["path"];
	        this.status = source["status"];
	        this.problems = source["problems"];
	        this.url = source["url"];
	    }
	}

}

export namespace domain {
	
	export class AccessEdit {
	    field: string;
	    note: string;
	    widens?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AccessEdit(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.field = source["field"];
	        this.note = source["note"];
	        this.widens = source["widens"];
	    }
	}
	export class Addressed {
	    item: string;
	    status: string;
	    note?: string;
	
	    static createFrom(source: any = {}) {
	        return new Addressed(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.item = source["item"];
	        this.status = source["status"];
	        this.note = source["note"];
	    }
	}
	export class ArtifactOutput {
	    path: string;
	    name?: string;
	    mediaType?: string;
	    description?: string;
	    digest?: string;
	
	    static createFrom(source: any = {}) {
	        return new ArtifactOutput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.name = source["name"];
	        this.mediaType = source["mediaType"];
	        this.description = source["description"];
	        this.digest = source["digest"];
	    }
	}
	export class ArtifactRef {
	    digest: string;
	    mediaType: string;
	    size: number;
	    name?: string;
	    kind?: string;
	
	    static createFrom(source: any = {}) {
	        return new ArtifactRef(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.digest = source["digest"];
	        this.mediaType = source["mediaType"];
	        this.size = source["size"];
	        this.name = source["name"];
	        this.kind = source["kind"];
	    }
	}
	export class ChangedFile {
	    path: string;
	    additions: number;
	    deletions: number;
	    binary?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ChangedFile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.additions = source["additions"];
	        this.deletions = source["deletions"];
	        this.binary = source["binary"];
	    }
	}
	export class BranchChanges {
	    base: string;
	    tip: string;
	    files?: ChangedFile[];
	    moreFiles?: number;
	
	    static createFrom(source: any = {}) {
	        return new BranchChanges(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.base = source["base"];
	        this.tip = source["tip"];
	        this.files = this.convertValues(source["files"], ChangedFile);
	        this.moreFiles = source["moreFiles"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class BranchPlan {
	    worktree: boolean;
	    branch?: string;
	    base: string;
	    root?: string;
	    cleanup: string;
	    maxLength?: number;
	    proposed?: Record<string, string>;
	    notes?: string[];
	
	    static createFrom(source: any = {}) {
	        return new BranchPlan(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.worktree = source["worktree"];
	        this.branch = source["branch"];
	        this.base = source["base"];
	        this.root = source["root"];
	        this.cleanup = source["cleanup"];
	        this.maxLength = source["maxLength"];
	        this.proposed = source["proposed"];
	        this.notes = source["notes"];
	    }
	}
	export class Budget {
	    wallTimeSeconds: number;
	    maxTurns?: number;
	    maxOutputBytes?: number;
	    maxCostCents?: number;
	
	    static createFrom(source: any = {}) {
	        return new Budget(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.wallTimeSeconds = source["wallTimeSeconds"];
	        this.maxTurns = source["maxTurns"];
	        this.maxOutputBytes = source["maxOutputBytes"];
	        this.maxCostCents = source["maxCostCents"];
	    }
	}
	export class Budgets {
	    objective?: number;
	    stagePrompt?: number;
	    acceptanceCriteria?: number;
	    constraints?: number;
	    dependencySummary?: number;
	    decisions?: number;
	    risks?: number;
	    skill?: number;
	    humanResponses?: number;
	    peerMessages?: number;
	    previousTurn?: number;
	    feedback?: number;
	    previousRound?: number;
	    envelope?: number;
	
	    static createFrom(source: any = {}) {
	        return new Budgets(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.objective = source["objective"];
	        this.stagePrompt = source["stagePrompt"];
	        this.acceptanceCriteria = source["acceptanceCriteria"];
	        this.constraints = source["constraints"];
	        this.dependencySummary = source["dependencySummary"];
	        this.decisions = source["decisions"];
	        this.risks = source["risks"];
	        this.skill = source["skill"];
	        this.humanResponses = source["humanResponses"];
	        this.peerMessages = source["peerMessages"];
	        this.previousTurn = source["previousTurn"];
	        this.feedback = source["feedback"];
	        this.previousRound = source["previousRound"];
	        this.envelope = source["envelope"];
	    }
	}
	export class CostTotal {
	    reportedCents: number;
	    estimatedCents: number;
	    unknownCalls: number;
	
	    static createFrom(source: any = {}) {
	        return new CostTotal(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.reportedCents = source["reportedCents"];
	        this.estimatedCents = source["estimatedCents"];
	        this.unknownCalls = source["unknownCalls"];
	    }
	}
	export class Usage {
	    inputTokens?: number;
	    cacheWriteTokens?: number;
	    longCacheWriteTokens?: number;
	    cacheReadTokens?: number;
	    outputTokens?: number;
	    reportedCents?: number;
	    premiumRequests?: number;
	    turns?: number;
	
	    static createFrom(source: any = {}) {
	        return new Usage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.inputTokens = source["inputTokens"];
	        this.cacheWriteTokens = source["cacheWriteTokens"];
	        this.longCacheWriteTokens = source["longCacheWriteTokens"];
	        this.cacheReadTokens = source["cacheReadTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.reportedCents = source["reportedCents"];
	        this.premiumRequests = source["premiumRequests"];
	        this.turns = source["turns"];
	    }
	}
	export class CallTotals {
	    usage: Usage;
	    cost: CostTotal;
	    bestCents: number;
	    subscriptionCents: number;
	    calls: number;
	    durationMs: number;
	
	    static createFrom(source: any = {}) {
	        return new CallTotals(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.usage = this.convertValues(source["usage"], Usage);
	        this.cost = this.convertValues(source["cost"], CostTotal);
	        this.bestCents = source["bestCents"];
	        this.subscriptionCents = source["subscriptionCents"];
	        this.calls = source["calls"];
	        this.durationMs = source["durationMs"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Change {
	    path: string;
	    summary: string;
	
	    static createFrom(source: any = {}) {
	        return new Change(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.summary = source["summary"];
	    }
	}
	
	export class ComplexityAssessment {
	    score: number;
	    rationale: string;
	    signals?: string[];
	
	    static createFrom(source: any = {}) {
	        return new ComplexityAssessment(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.score = source["score"];
	        this.rationale = source["rationale"];
	        this.signals = source["signals"];
	    }
	}
	export class TrackerGrant {
	    tracker: string;
	    kinds: string[];
	    story?: string;
	
	    static createFrom(source: any = {}) {
	        return new TrackerGrant(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tracker = source["tracker"];
	        this.kinds = source["kinds"];
	        this.story = source["story"];
	    }
	}
	export class UserMCPServer {
	    name: string;
	    source?: string;
	    transport?: string;
	    url?: string;
	    command?: string;
	    status: string;
	    detail?: string;
	
	    static createFrom(source: any = {}) {
	        return new UserMCPServer(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.source = source["source"];
	        this.transport = source["transport"];
	        this.url = source["url"];
	        this.command = source["command"];
	        this.status = source["status"];
	        this.detail = source["detail"];
	    }
	}
	export class MCPServer {
	    name: string;
	    description?: string;
	    command?: string[];
	    url?: string;
	    tools?: string[];
	    tracker?: string;
	
	    static createFrom(source: any = {}) {
	        return new MCPServer(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.command = source["command"];
	        this.url = source["url"];
	        this.tools = source["tools"];
	        this.tracker = source["tracker"];
	    }
	}
	export class ResumeNote {
	    round: number;
	    stopped: string;
	    reason?: string;
	    kept?: string[];
	    moreKept?: number;
	    discarded?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ResumeNote(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.round = source["round"];
	        this.stopped = source["stopped"];
	        this.reason = source["reason"];
	        this.kept = source["kept"];
	        this.moreKept = source["moreKept"];
	        this.discarded = source["discarded"];
	    }
	}
	export class RoundDigest {
	    round: number;
	    summary?: string;
	    changes?: string[];
	    moreChanges?: number;
	    replies?: string[];
	    earlier?: string[];
	    moreEarlier?: number;
	
	    static createFrom(source: any = {}) {
	        return new RoundDigest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.round = source["round"];
	        this.summary = source["summary"];
	        this.changes = source["changes"];
	        this.moreChanges = source["moreChanges"];
	        this.replies = source["replies"];
	        this.earlier = source["earlier"];
	        this.moreEarlier = source["moreEarlier"];
	    }
	}
	export class FailedCheck {
	    command: string[];
	    exitCode?: number;
	    error?: string;
	    outputTail?: string;
	
	    static createFrom(source: any = {}) {
	        return new FailedCheck(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.command = source["command"];
	        this.exitCode = source["exitCode"];
	        this.error = source["error"];
	        this.outputTail = source["outputTail"];
	    }
	}
	export class LineComment {
	    path: string;
	    line: number;
	    endLine?: number;
	    side: string;
	    excerpt?: string;
	    thread?: string;
	
	    static createFrom(source: any = {}) {
	        return new LineComment(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.line = source["line"];
	        this.endLine = source["endLine"];
	        this.side = source["side"];
	        this.excerpt = source["excerpt"];
	        this.thread = source["thread"];
	    }
	}
	export class FeedbackItem {
	    id: string;
	    from: string;
	    text: string;
	    comment?: LineComment;
	    check?: FailedCheck;
	
	    static createFrom(source: any = {}) {
	        return new FeedbackItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.from = source["from"];
	        this.text = source["text"];
	        this.comment = this.convertValues(source["comment"], LineComment);
	        this.check = this.convertValues(source["check"], FailedCheck);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RoundFeedback {
	    round: number;
	    items: FeedbackItem[];
	    changes?: BranchChanges;
	
	    static createFrom(source: any = {}) {
	        return new RoundFeedback(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.round = source["round"];
	        this.items = this.convertValues(source["items"], FeedbackItem);
	        this.changes = this.convertValues(source["changes"], BranchChanges);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class TurnDigest {
	    turn: number;
	    stoppedFor: string;
	    summary?: string;
	    changes?: string[];
	    moreChanges?: number;
	
	    static createFrom(source: any = {}) {
	        return new TurnDigest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.turn = source["turn"];
	        this.stoppedFor = source["stoppedFor"];
	        this.summary = source["summary"];
	        this.changes = source["changes"];
	        this.moreChanges = source["moreChanges"];
	    }
	}
	export class SkillBrief {
	    id: string;
	    description?: string;
	    path: string;
	
	    static createFrom(source: any = {}) {
	        return new SkillBrief(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.description = source["description"];
	        this.path = source["path"];
	    }
	}
	export class DocBrief {
	    path: string;
	    title?: string;
	    tags?: string[];
	    status?: string;
	    lastUpdated?: string;
	    author?: string;
	    problems?: string[];
	
	    static createFrom(source: any = {}) {
	        return new DocBrief(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.title = source["title"];
	        this.tags = source["tags"];
	        this.status = source["status"];
	        this.lastUpdated = source["lastUpdated"];
	        this.author = source["author"];
	        this.problems = source["problems"];
	    }
	}
	export class ModelSelection {
	    model: string;
	    reasoningEffort?: string;
	    policyVersion?: number;
	    source: string;
	    rationale: string;
	
	    static createFrom(source: any = {}) {
	        return new ModelSelection(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.model = source["model"];
	        this.reasoningEffort = source["reasoningEffort"];
	        this.policyVersion = source["policyVersion"];
	        this.source = source["source"];
	        this.rationale = source["rationale"];
	    }
	}
	export class PeerMessage {
	    from: string;
	    to: string;
	    subject?: string;
	    content: string;
	
	    static createFrom(source: any = {}) {
	        return new PeerMessage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.from = source["from"];
	        this.to = source["to"];
	        this.subject = source["subject"];
	        this.content = source["content"];
	    }
	}
	export class HumanResponse {
	    question: string;
	    answer: string;
	
	    static createFrom(source: any = {}) {
	        return new HumanResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.question = source["question"];
	        this.answer = source["answer"];
	    }
	}
	export class ContextEnvelope {
	    version: number;
	    taskId: string;
	    stepId: string;
	    runtime: string;
	    agent?: string;
	    agentInstructions?: string;
	    humanResponses?: HumanResponse[];
	    peerMessages?: PeerMessage[];
	    complexity?: ComplexityAssessment;
	    modelSelection?: ModelSelection;
	    title?: string;
	    objective: string;
	    acceptanceCriteria: string[];
	    allowedPaths?: string[];
	    relevantDecisions?: string[];
	    relevantRisks?: string[];
	    dependencySummary?: string[];
	    artifacts?: ArtifactRef[];
	    skillExcerpts?: Record<string, string>;
	    commands?: string[][];
	    budget: Budget;
	    metadata?: Record<string, string>;
	    docs?: DocBrief[];
	    dependencyOutputs?: ArtifactOutput[];
	    skillIndex?: SkillBrief[];
	    previousTurn?: TurnDigest;
	    feedback?: RoundFeedback;
	    previousRound?: RoundDigest;
	    resumed?: ResumeNote;
	    stageTitle?: string;
	    stagePrompt?: string;
	    constraints?: string[];
	    mcpServers?: MCPServer[];
	    userMcpServers?: UserMCPServer[];
	    tracker?: TrackerGrant;
	
	    static createFrom(source: any = {}) {
	        return new ContextEnvelope(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.taskId = source["taskId"];
	        this.stepId = source["stepId"];
	        this.runtime = source["runtime"];
	        this.agent = source["agent"];
	        this.agentInstructions = source["agentInstructions"];
	        this.humanResponses = this.convertValues(source["humanResponses"], HumanResponse);
	        this.peerMessages = this.convertValues(source["peerMessages"], PeerMessage);
	        this.complexity = this.convertValues(source["complexity"], ComplexityAssessment);
	        this.modelSelection = this.convertValues(source["modelSelection"], ModelSelection);
	        this.title = source["title"];
	        this.objective = source["objective"];
	        this.acceptanceCriteria = source["acceptanceCriteria"];
	        this.allowedPaths = source["allowedPaths"];
	        this.relevantDecisions = source["relevantDecisions"];
	        this.relevantRisks = source["relevantRisks"];
	        this.dependencySummary = source["dependencySummary"];
	        this.artifacts = this.convertValues(source["artifacts"], ArtifactRef);
	        this.skillExcerpts = source["skillExcerpts"];
	        this.commands = source["commands"];
	        this.budget = this.convertValues(source["budget"], Budget);
	        this.metadata = source["metadata"];
	        this.docs = this.convertValues(source["docs"], DocBrief);
	        this.dependencyOutputs = this.convertValues(source["dependencyOutputs"], ArtifactOutput);
	        this.skillIndex = this.convertValues(source["skillIndex"], SkillBrief);
	        this.previousTurn = this.convertValues(source["previousTurn"], TurnDigest);
	        this.feedback = this.convertValues(source["feedback"], RoundFeedback);
	        this.previousRound = this.convertValues(source["previousRound"], RoundDigest);
	        this.resumed = this.convertValues(source["resumed"], ResumeNote);
	        this.stageTitle = source["stageTitle"];
	        this.stagePrompt = source["stagePrompt"];
	        this.constraints = source["constraints"];
	        this.mcpServers = this.convertValues(source["mcpServers"], MCPServer);
	        this.userMcpServers = this.convertValues(source["userMcpServers"], UserMCPServer);
	        this.tracker = this.convertValues(source["tracker"], TrackerGrant);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Cost {
	    cents: number;
	    source: string;
	    estimatedCents?: number;
	    pricingVersion?: number;
	
	    static createFrom(source: any = {}) {
	        return new Cost(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.cents = source["cents"];
	        this.source = source["source"];
	        this.estimatedCents = source["estimatedCents"];
	        this.pricingVersion = source["pricingVersion"];
	    }
	}
	export class CostGroup {
	    key: string;
	    total: CallTotals;
	
	    static createFrom(source: any = {}) {
	        return new CostGroup(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.total = this.convertValues(source["total"], CallTotals);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	export class DocRead {
	    taskId: string;
	    stepId: string;
	    runId: string;
	    projectId: string;
	    agent: string;
	    runtime: string;
	    docsDir: string;
	    docPath: string;
	    tool: string;
	    // Go type: time
	    openedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new DocRead(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.taskId = source["taskId"];
	        this.stepId = source["stepId"];
	        this.runId = source["runId"];
	        this.projectId = source["projectId"];
	        this.agent = source["agent"];
	        this.runtime = source["runtime"];
	        this.docsDir = source["docsDir"];
	        this.docPath = source["docPath"];
	        this.tool = source["tool"];
	        this.openedAt = this.convertValues(source["openedAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	export class FileRead {
	    tool: string;
	    paths: string[];
	
	    static createFrom(source: any = {}) {
	        return new FileRead(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tool = source["tool"];
	        this.paths = source["paths"];
	    }
	}
	export class TrackerChange {
	    kind: string;
	    issue?: string;
	    body?: string;
	    status?: string;
	    title?: string;
	    description?: string;
	    addLabels?: string[];
	    removeLabels?: string[];
	    estimate?: number;
	    priority?: number;
	    assignee?: string;
	    cycle?: string;
	    milestone?: string;
	    parent?: string;
	
	    static createFrom(source: any = {}) {
	        return new TrackerChange(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.issue = source["issue"];
	        this.body = source["body"];
	        this.status = source["status"];
	        this.title = source["title"];
	        this.description = source["description"];
	        this.addLabels = source["addLabels"];
	        this.removeLabels = source["removeLabels"];
	        this.estimate = source["estimate"];
	        this.priority = source["priority"];
	        this.assignee = source["assignee"];
	        this.cycle = source["cycle"];
	        this.milestone = source["milestone"];
	        this.parent = source["parent"];
	    }
	}
	export class HumanInputRequest {
	    question: string;
	    options?: string[];
	    context?: string;
	
	    static createFrom(source: any = {}) {
	        return new HumanInputRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.question = source["question"];
	        this.options = source["options"];
	        this.context = source["context"];
	    }
	}
	export class HandoffResult {
	    version: number;
	    status: string;
	    summary: string;
	    changes?: Change[];
	    verification?: string[];
	    decisions?: string[];
	    risks?: string[];
	    document?: string;
	    usage?: Usage;
	    artifacts?: ArtifactRef[];
	    outputs?: ArtifactOutput[];
	    messages?: PeerMessage[];
	    humanInput?: HumanInputRequest;
	    trackerChanges?: TrackerChange[];
	    addressed?: Addressed[];
	    metadata?: Record<string, string>;
	
	    static createFrom(source: any = {}) {
	        return new HandoffResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.status = source["status"];
	        this.summary = source["summary"];
	        this.changes = this.convertValues(source["changes"], Change);
	        this.verification = source["verification"];
	        this.decisions = source["decisions"];
	        this.risks = source["risks"];
	        this.document = source["document"];
	        this.usage = this.convertValues(source["usage"], Usage);
	        this.artifacts = this.convertValues(source["artifacts"], ArtifactRef);
	        this.outputs = this.convertValues(source["outputs"], ArtifactOutput);
	        this.messages = this.convertValues(source["messages"], PeerMessage);
	        this.humanInput = this.convertValues(source["humanInput"], HumanInputRequest);
	        this.trackerChanges = this.convertValues(source["trackerChanges"], TrackerChange);
	        this.addressed = this.convertValues(source["addressed"], Addressed);
	        this.metadata = source["metadata"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	
	
	
	
	export class TaskCostSummary {
	    taskId: string;
	    projectId: string;
	    title: string;
	    objective: string;
	    kind: string;
	    state: string;
	    latestRunId?: string;
	    // Go type: time
	    lastCallAt: any;
	    total: CallTotals;
	
	    static createFrom(source: any = {}) {
	        return new TaskCostSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.taskId = source["taskId"];
	        this.projectId = source["projectId"];
	        this.title = source["title"];
	        this.objective = source["objective"];
	        this.kind = source["kind"];
	        this.state = source["state"];
	        this.latestRunId = source["latestRunId"];
	        this.lastCallAt = this.convertValues(source["lastCallAt"], null);
	        this.total = this.convertValues(source["total"], CallTotals);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class PeriodCost {
	    tasks: TaskCostSummary[];
	    total: CallTotals;
	
	    static createFrom(source: any = {}) {
	        return new PeriodCost(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tasks = this.convertValues(source["tasks"], TaskCostSummary);
	        this.total = this.convertValues(source["total"], CallTotals);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class PromptSection {
	    section: string;
	    name?: string;
	    bytes: number;
	    truncated?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new PromptSection(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.section = source["section"];
	        this.name = source["name"];
	        this.bytes = source["bytes"];
	        this.truncated = source["truncated"];
	    }
	}
	export class UsageRecord {
	    id: number;
	    taskId: string;
	    executionId?: string;
	    stepId: string;
	    callId: string;
	    turn: number;
	    round?: number;
	    kind: string;
	    agent: string;
	    runtime: string;
	    model?: string;
	    modelUsed?: string;
	    authMode?: string;
	    usage: Usage;
	    cost: Cost;
	    promptBytes: number;
	    sections?: PromptSection[];
	    promptDigest?: string;
	    outcome: string;
	    // Go type: time
	    startedAt: any;
	    // Go type: time
	    finishedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new UsageRecord(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.taskId = source["taskId"];
	        this.executionId = source["executionId"];
	        this.stepId = source["stepId"];
	        this.callId = source["callId"];
	        this.turn = source["turn"];
	        this.round = source["round"];
	        this.kind = source["kind"];
	        this.agent = source["agent"];
	        this.runtime = source["runtime"];
	        this.model = source["model"];
	        this.modelUsed = source["modelUsed"];
	        this.authMode = source["authMode"];
	        this.usage = this.convertValues(source["usage"], Usage);
	        this.cost = this.convertValues(source["cost"], Cost);
	        this.promptBytes = source["promptBytes"];
	        this.sections = this.convertValues(source["sections"], PromptSection);
	        this.promptDigest = source["promptDigest"];
	        this.outcome = source["outcome"];
	        this.startedAt = this.convertValues(source["startedAt"], null);
	        this.finishedAt = this.convertValues(source["finishedAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class PlanningCost {
	    total: CallTotals;
	    calls: UsageRecord[];
	
	    static createFrom(source: any = {}) {
	        return new PlanningCost(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.total = this.convertValues(source["total"], CallTotals);
	        this.calls = this.convertValues(source["calls"], UsageRecord);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	
	
	export class StepCost {
	    stepId: string;
	    agent: string;
	    runtime: string;
	    model: string;
	    // Go type: time
	    startedAt: any;
	    total: CallTotals;
	    calls: UsageRecord[];
	
	    static createFrom(source: any = {}) {
	        return new StepCost(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.stepId = source["stepId"];
	        this.agent = source["agent"];
	        this.runtime = source["runtime"];
	        this.model = source["model"];
	        this.startedAt = this.convertValues(source["startedAt"], null);
	        this.total = this.convertValues(source["total"], CallTotals);
	        this.calls = this.convertValues(source["calls"], UsageRecord);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RunCost {
	    executionId: string;
	    state: string;
	    // Go type: time
	    startedAt: any;
	    // Go type: time
	    finishedAt: any;
	    total: CallTotals;
	    steps: StepCost[];
	
	    static createFrom(source: any = {}) {
	        return new RunCost(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.executionId = source["executionId"];
	        this.state = source["state"];
	        this.startedAt = this.convertValues(source["startedAt"], null);
	        this.finishedAt = this.convertValues(source["finishedAt"], null);
	        this.total = this.convertValues(source["total"], CallTotals);
	        this.steps = this.convertValues(source["steps"], StepCost);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RunEvent {
	    version: number;
	    runId: string;
	    sequence: number;
	    // Go type: time
	    timestamp: any;
	    kind: string;
	    summary: string;
	    payload?: Record<string, any>;
	    artifact?: ArtifactRef;
	    usage?: Usage;
	    reads?: FileRead[];
	
	    static createFrom(source: any = {}) {
	        return new RunEvent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.runId = source["runId"];
	        this.sequence = source["sequence"];
	        this.timestamp = this.convertValues(source["timestamp"], null);
	        this.kind = source["kind"];
	        this.summary = source["summary"];
	        this.payload = source["payload"];
	        this.artifact = this.convertValues(source["artifact"], ArtifactRef);
	        this.usage = this.convertValues(source["usage"], Usage);
	        this.reads = this.convertValues(source["reads"], FileRead);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class SourceSnapshot {
	    source: string;
	    version: string;
	    // Go type: time
	    fetchedAt: any;
	    cached?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SourceSnapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.source = source["source"];
	        this.version = source["version"];
	        this.fetchedAt = this.convertValues(source["fetchedAt"], null);
	        this.cached = source["cached"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class StepAccess {
	    permission?: string;
	    commands?: string[][];
	    allowedPaths?: string[];
	
	    static createFrom(source: any = {}) {
	        return new StepAccess(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.permission = source["permission"];
	        this.commands = source["commands"];
	        this.allowedPaths = source["allowedPaths"];
	    }
	}
	
	export class StepOrigin {
	    stage?: string;
	    groups?: string[];
	    scope: string;
	    file?: string;
	    overrides?: string[];
	
	    static createFrom(source: any = {}) {
	        return new StepOrigin(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.stage = source["stage"];
	        this.groups = source["groups"];
	        this.scope = source["scope"];
	        this.file = source["file"];
	        this.overrides = source["overrides"];
	    }
	}
	export class TaskCost {
	    taskId: string;
	    total: CallTotals;
	    planning: PlanningCost;
	    runs: RunCost[];
	    byAgent: CostGroup[];
	    byRuntime: CostGroup[];
	    byModel: CostGroup[];
	    byStep: CostGroup[];
	
	    static createFrom(source: any = {}) {
	        return new TaskCost(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.taskId = source["taskId"];
	        this.total = this.convertValues(source["total"], CallTotals);
	        this.planning = this.convertValues(source["planning"], PlanningCost);
	        this.runs = this.convertValues(source["runs"], RunCost);
	        this.byAgent = this.convertValues(source["byAgent"], CostGroup);
	        this.byRuntime = this.convertValues(source["byRuntime"], CostGroup);
	        this.byModel = this.convertValues(source["byModel"], CostGroup);
	        this.byStep = this.convertValues(source["byStep"], CostGroup);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class TaskSource {
	    kind: string;
	    reference?: string;
	    repository?: string;
	
	    static createFrom(source: any = {}) {
	        return new TaskSource(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.reference = source["reference"];
	        this.repository = source["repository"];
	    }
	}
	export class TaskSpec {
	    version: number;
	    id: string;
	    kind: string;
	    title?: string;
	    objective: string;
	    source: TaskSource;
	    acceptanceCriteria: string[];
	    constraints?: string[];
	    labels?: string[];
	    repository: string;
	    priority: number;
	    budget: Budget;
	    metadata?: Record<string, string>;
	
	    static createFrom(source: any = {}) {
	        return new TaskSpec(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.id = source["id"];
	        this.kind = source["kind"];
	        this.title = source["title"];
	        this.objective = source["objective"];
	        this.source = this.convertValues(source["source"], TaskSource);
	        this.acceptanceCriteria = source["acceptanceCriteria"];
	        this.constraints = source["constraints"];
	        this.labels = source["labels"];
	        this.repository = source["repository"];
	        this.priority = source["priority"];
	        this.budget = this.convertValues(source["budget"], Budget);
	        this.metadata = source["metadata"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	
	
	
	
	export class WorkflowStep {
	    id: string;
	    title: string;
	    kind: string;
	    dependsOn?: string[];
	    runtime?: string;
	    agent?: string;
	    prompt?: string;
	    complexity?: ComplexityAssessment;
	    modelSelection?: ModelSelection;
	    permission: string;
	    selectedSkills?: string[];
	    allowedPaths?: string[];
	    commands?: string[][];
	    continueOnFailure?: boolean;
	    expectedOutputs?: string[];
	    budget: Budget;
	    metadata?: Record<string, string>;
	    review?: boolean;
	    onFailure?: string;
	    feedbackTarget?: string;
	    maxRounds?: number;
	    systemExitCodes?: number[];
	    mcpServers?: string[];
	    userMcp?: string[];
	    trackerKinds?: string[];
	    accessEdits?: AccessEdit[];
	    ruleAccess?: StepAccess;
	    from?: StepOrigin;
	
	    static createFrom(source: any = {}) {
	        return new WorkflowStep(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.kind = source["kind"];
	        this.dependsOn = source["dependsOn"];
	        this.runtime = source["runtime"];
	        this.agent = source["agent"];
	        this.prompt = source["prompt"];
	        this.complexity = this.convertValues(source["complexity"], ComplexityAssessment);
	        this.modelSelection = this.convertValues(source["modelSelection"], ModelSelection);
	        this.permission = source["permission"];
	        this.selectedSkills = source["selectedSkills"];
	        this.allowedPaths = source["allowedPaths"];
	        this.commands = source["commands"];
	        this.continueOnFailure = source["continueOnFailure"];
	        this.expectedOutputs = source["expectedOutputs"];
	        this.budget = this.convertValues(source["budget"], Budget);
	        this.metadata = source["metadata"];
	        this.review = source["review"];
	        this.onFailure = source["onFailure"];
	        this.feedbackTarget = source["feedbackTarget"];
	        this.maxRounds = source["maxRounds"];
	        this.systemExitCodes = source["systemExitCodes"];
	        this.mcpServers = source["mcpServers"];
	        this.userMcp = source["userMcp"];
	        this.trackerKinds = source["trackerKinds"];
	        this.accessEdits = this.convertValues(source["accessEdits"], AccessEdit);
	        this.ruleAccess = this.convertValues(source["ruleAccess"], StepAccess);
	        this.from = this.convertValues(source["from"], StepOrigin);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class WorkflowSpec {
	    version: number;
	    id: string;
	    taskId: string;
	    defaultRuntime: string;
	    routingPolicyVersion?: number;
	    ruleName?: string;
	    orchestrated?: boolean;
	    maxConcurrency: number;
	    steps: WorkflowStep[];
	    branching?: BranchPlan;
	    mcpServerDigests?: Record<string, string>;
	    stageDigests?: Record<string, string>;
	    userMcp?: Record<string, Array<string>>;
	    trackerKinds?: string[];
	    sources?: SourceSnapshot[];
	    // Go type: time
	    createdAt: any;
	
	    static createFrom(source: any = {}) {
	        return new WorkflowSpec(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.id = source["id"];
	        this.taskId = source["taskId"];
	        this.defaultRuntime = source["defaultRuntime"];
	        this.routingPolicyVersion = source["routingPolicyVersion"];
	        this.ruleName = source["ruleName"];
	        this.orchestrated = source["orchestrated"];
	        this.maxConcurrency = source["maxConcurrency"];
	        this.steps = this.convertValues(source["steps"], WorkflowStep);
	        this.branching = this.convertValues(source["branching"], BranchPlan);
	        this.mcpServerDigests = source["mcpServerDigests"];
	        this.stageDigests = source["stageDigests"];
	        this.userMcp = source["userMcp"];
	        this.trackerKinds = source["trackerKinds"];
	        this.sources = this.convertValues(source["sources"], SourceSnapshot);
	        this.createdAt = this.convertValues(source["createdAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace github {
	
	export class PullRequest {
	    number: number;
	    title: string;
	    url: string;
	    state: string;
	    mergedAt?: string;
	    headRefName?: string;
	    isCrossRepository?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new PullRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.number = source["number"];
	        this.title = source["title"];
	        this.url = source["url"];
	        this.state = source["state"];
	        this.mergedAt = source["mergedAt"];
	        this.headRefName = source["headRefName"];
	        this.isCrossRepository = source["isCrossRepository"];
	    }
	}

}

export namespace orchestrator {
	
	export class Assignment {
	    stageId: string;
	    agent: string;
	    skills: string[];
	    complexity: domain.ComplexityAssessment;
	    rationale: string;
	
	    static createFrom(source: any = {}) {
	        return new Assignment(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.stageId = source["stageId"];
	        this.agent = source["agent"];
	        this.skills = source["skills"];
	        this.complexity = this.convertValues(source["complexity"], domain.ComplexityAssessment);
	        this.rationale = source["rationale"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Decision {
	    assignments: Assignment[];
	    notes?: string;
	
	    static createFrom(source: any = {}) {
	        return new Decision(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.assignments = this.convertValues(source["assignments"], Assignment);
	        this.notes = source["notes"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace pricing {
	
	export class Rates {
	    input?: number;
	    cacheWrite?: number;
	    longCacheWrite?: number;
	    cacheRead?: number;
	    output?: number;
	
	    static createFrom(source: any = {}) {
	        return new Rates(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.input = source["input"];
	        this.cacheWrite = source["cacheWrite"];
	        this.longCacheWrite = source["longCacheWrite"];
	        this.cacheRead = source["cacheRead"];
	        this.output = source["output"];
	    }
	}
	export class Overrides {
	    models?: Record<string, Rates>;
	    premiumRequest?: number;
	
	    static createFrom(source: any = {}) {
	        return new Overrides(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.models = this.convertValues(source["models"], Rates, true);
	        this.premiumRequest = source["premiumRequest"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Price {
	    provider?: string;
	    input: number;
	    cacheWrite: number;
	    longCacheWrite: number;
	    cacheRead: number;
	    output: number;
	    source: string;
	    asOf?: string;
	    note?: string;
	
	    static createFrom(source: any = {}) {
	        return new Price(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.provider = source["provider"];
	        this.input = source["input"];
	        this.cacheWrite = source["cacheWrite"];
	        this.longCacheWrite = source["longCacheWrite"];
	        this.cacheRead = source["cacheRead"];
	        this.output = source["output"];
	        this.source = source["source"];
	        this.asOf = source["asOf"];
	        this.note = source["note"];
	    }
	}
	
	export class Table {
	    version: number;
	    models: Record<string, Price>;
	    premiumRequest?: number;
	
	    static createFrom(source: any = {}) {
	        return new Table(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.models = this.convertValues(source["models"], Price, true);
	        this.premiumRequest = source["premiumRequest"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace remote {
	
	export class SecurityScheme {
	    type: string;
	    scheme?: string;
	    in?: string;
	    name?: string;
	
	    static createFrom(source: any = {}) {
	        return new SecurityScheme(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.scheme = source["scheme"];
	        this.in = source["in"];
	        this.name = source["name"];
	    }
	}
	export class Card {
	    name: string;
	    description: string;
	    url: string;
	    version: string;
	    protocolVersion: string;
	    provider?: a2a.AgentProvider;
	    capabilities: a2a.AgentCapabilities;
	    defaultInputModes: string[];
	    defaultOutputModes: string[];
	    skills: a2a.AgentSkill[];
	    metadata?: Record<string, any>;
	    preferredTransport?: string;
	    securitySchemes?: Record<string, SecurityScheme>;
	    security?: any[];
	    digest: string;
	
	    static createFrom(source: any = {}) {
	        return new Card(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.url = source["url"];
	        this.version = source["version"];
	        this.protocolVersion = source["protocolVersion"];
	        this.provider = this.convertValues(source["provider"], a2a.AgentProvider);
	        this.capabilities = this.convertValues(source["capabilities"], a2a.AgentCapabilities);
	        this.defaultInputModes = source["defaultInputModes"];
	        this.defaultOutputModes = source["defaultOutputModes"];
	        this.skills = this.convertValues(source["skills"], a2a.AgentSkill);
	        this.metadata = source["metadata"];
	        this.preferredTransport = source["preferredTransport"];
	        this.securitySchemes = this.convertValues(source["securitySchemes"], SecurityScheme, true);
	        this.security = source["security"];
	        this.digest = source["digest"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Pin {
	    digest: string;
	    url: string;
	    protocolVersion: string;
	    streaming?: boolean;
	    auth?: string;
	    fetchedAt?: string;
	
	    static createFrom(source: any = {}) {
	        return new Pin(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.digest = source["digest"];
	        this.url = source["url"];
	        this.protocolVersion = source["protocolVersion"];
	        this.streaming = source["streaming"];
	        this.auth = source["auth"];
	        this.fetchedAt = source["fetchedAt"];
	    }
	}

}

export namespace routing {
	
	export class Band {
	    minimum: number;
	    maximum: number;
	    model: string;
	    reasoningEffort?: string;
	
	    static createFrom(source: any = {}) {
	        return new Band(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.minimum = source["minimum"];
	        this.maximum = source["maximum"];
	        this.model = source["model"];
	        this.reasoningEffort = source["reasoningEffort"];
	    }
	}
	export class Policy {
	    version: number;
	    bands: Record<string, Array<Band>>;
	
	    static createFrom(source: any = {}) {
	        return new Policy(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.bands = this.convertValues(source["bands"], Array<Band>, true);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace rules {
	
	export class Range {
	    min: number;
	    max: number;
	
	    static createFrom(source: any = {}) {
	        return new Range(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.min = source["min"];
	        this.max = source["max"];
	    }
	}
	export class Condition {
	    kind?: string[];
	    labels?: string[];
	    source?: string[];
	    keywords?: string[];
	    repositories?: string[];
	    complexity?: Range;
	    minPriority?: number;
	
	    static createFrom(source: any = {}) {
	        return new Condition(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.labels = source["labels"];
	        this.source = source["source"];
	        this.keywords = source["keywords"];
	        this.repositories = source["repositories"];
	        this.complexity = this.convertValues(source["complexity"], Range);
	        this.minPriority = source["minPriority"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Flow {
	    stages: Stage[];
	
	    static createFrom(source: any = {}) {
	        return new Flow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.stages = this.convertValues(source["stages"], Stage);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Rule {
	    name: string;
	    description?: string;
	    when: Condition;
	    then: Flow;
	
	    static createFrom(source: any = {}) {
	        return new Rule(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.when = this.convertValues(source["when"], Condition);
	        this.then = this.convertValues(source["then"], Flow);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Group {
	    stages: Stage[];
	
	    static createFrom(source: any = {}) {
	        return new Group(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.stages = this.convertValues(source["stages"], Stage);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Provenance {
	    stage?: string;
	    groups?: string[];
	    scope: string;
	    file?: string;
	    overrides?: string[];
	
	    static createFrom(source: any = {}) {
	        return new Provenance(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.stage = source["stage"];
	        this.groups = source["groups"];
	        this.scope = source["scope"];
	        this.file = source["file"];
	        this.overrides = source["overrides"];
	    }
	}
	export class Feedback {
	    target?: string;
	    maxRounds?: number;
	    systemExitCodes?: number[];
	
	    static createFrom(source: any = {}) {
	        return new Feedback(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.target = source["target"];
	        this.maxRounds = source["maxRounds"];
	        this.systemExitCodes = source["systemExitCodes"];
	    }
	}
	export class Stage {
	    use?: string;
	    id: string;
	    title?: string;
	    kind: string;
	    agent?: string;
	    runtime?: string;
	    permission?: string;
	    dependsOn?: string[];
	    skills?: string[];
	    allowedPaths?: string[];
	    prompt?: string;
	    commands?: string[][];
	    optional?: boolean;
	    continueOnFailure?: boolean;
	    review?: boolean;
	    onFailure?: string;
	    feedback?: Feedback;
	    mcp?: string[];
	    tracker?: string[];
	    overrides?: string[];
	    from?: Provenance;
	
	    static createFrom(source: any = {}) {
	        return new Stage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.use = source["use"];
	        this.id = source["id"];
	        this.title = source["title"];
	        this.kind = source["kind"];
	        this.agent = source["agent"];
	        this.runtime = source["runtime"];
	        this.permission = source["permission"];
	        this.dependsOn = source["dependsOn"];
	        this.skills = source["skills"];
	        this.allowedPaths = source["allowedPaths"];
	        this.prompt = source["prompt"];
	        this.commands = source["commands"];
	        this.optional = source["optional"];
	        this.continueOnFailure = source["continueOnFailure"];
	        this.review = source["review"];
	        this.onFailure = source["onFailure"];
	        this.feedback = this.convertValues(source["feedback"], Feedback);
	        this.mcp = source["mcp"];
	        this.tracker = source["tracker"];
	        this.overrides = source["overrides"];
	        this.from = this.convertValues(source["from"], Provenance);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Document {
	    version: number;
	    stages?: Record<string, Stage>;
	    groups?: Record<string, Group>;
	    rules: Rule[];
	    default?: Flow;
	    source: string;
	
	    static createFrom(source: any = {}) {
	        return new Document(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.stages = this.convertValues(source["stages"], Stage, true);
	        this.groups = this.convertValues(source["groups"], Group, true);
	        this.rules = this.convertValues(source["rules"], Rule);
	        this.default = this.convertValues(source["default"], Flow);
	        this.source = source["source"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	
	export class LibraryProblem {
	    file: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new LibraryProblem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.file = source["file"];
	        this.message = source["message"];
	    }
	}
	export class OverridableField {
	    name: string;
	    kinds?: string[];
	
	    static createFrom(source: any = {}) {
	        return new OverridableField(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.kinds = source["kinds"];
	    }
	}
	
	
	

}

export namespace runtime {
	
	export class UserServer {
	    name: string;
	    source?: string;
	    transport?: string;
	    url?: string;
	    command?: string;
	    status: string;
	    detail?: string;
	    signIn?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new UserServer(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.source = source["source"];
	        this.transport = source["transport"];
	        this.url = source["url"];
	        this.command = source["command"];
	        this.status = source["status"];
	        this.detail = source["detail"];
	        this.signIn = source["signIn"];
	    }
	}

}

export namespace secrets {
	
	export class Status {
	    name: string;
	    source: string;
	    available: boolean;
	    detail?: string;
	
	    static createFrom(source: any = {}) {
	        return new Status(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.source = source["source"];
	        this.available = source["available"];
	        this.detail = source["detail"];
	    }
	}

}

export namespace sources {
	
	export class DefinitionReport {
	    kind: string;
	    name: string;
	    mode: string;
	    scope?: string;
	    version?: string;
	    status: string;
	    reason?: string;
	
	    static createFrom(source: any = {}) {
	        return new DefinitionReport(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.name = source["name"];
	        this.mode = source["mode"];
	        this.scope = source["scope"];
	        this.version = source["version"];
	        this.status = source["status"];
	        this.reason = source["reason"];
	    }
	}
	export class Report {
	    source: string;
	    version?: string;
	    fetchedAt?: string;
	    cached?: boolean;
	    unreachable?: string;
	    definitions: DefinitionReport[];
	
	    static createFrom(source: any = {}) {
	        return new Report(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.source = source["source"];
	        this.version = source["version"];
	        this.fetchedAt = source["fetchedAt"];
	        this.cached = source["cached"];
	        this.unreachable = source["unreachable"];
	        this.definitions = this.convertValues(source["definitions"], DefinitionReport);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace storage {
	
	export class Comment {
	    id: string;
	    threadId: string;
	    body: string;
	    target: string;
	    state: string;
	    round?: number;
	    item?: string;
	    status?: string;
	    note?: string;
	    excerpt?: string;
	    // Go type: time
	    createdAt: any;
	    // Go type: time
	    updatedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new Comment(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.threadId = source["threadId"];
	        this.body = source["body"];
	        this.target = source["target"];
	        this.state = source["state"];
	        this.round = source["round"];
	        this.item = source["item"];
	        this.status = source["status"];
	        this.note = source["note"];
	        this.excerpt = source["excerpt"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
	        this.updatedAt = this.convertValues(source["updatedAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class FeedbackItem {
	    executionId: string;
	    round: number;
	    id: string;
	    target: string;
	    from: string;
	    text: string;
	    status: string;
	    note?: string;
	    // Go type: time
	    createdAt: any;
	    checkId?: string;
	    tail?: string;
	    commentId?: string;
	    path?: string;
	    side?: string;
	    line?: number;
	    endLine?: number;
	    commit?: string;
	    excerpt?: string;
	
	    static createFrom(source: any = {}) {
	        return new FeedbackItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.executionId = source["executionId"];
	        this.round = source["round"];
	        this.id = source["id"];
	        this.target = source["target"];
	        this.from = source["from"];
	        this.text = source["text"];
	        this.status = source["status"];
	        this.note = source["note"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
	        this.checkId = source["checkId"];
	        this.tail = source["tail"];
	        this.commentId = source["commentId"];
	        this.path = source["path"];
	        this.side = source["side"];
	        this.line = source["line"];
	        this.endLine = source["endLine"];
	        this.commit = source["commit"];
	        this.excerpt = source["excerpt"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class PermissionAsk {
	    command?: string;
	    tool?: string;
	    detail?: string;
	
	    static createFrom(source: any = {}) {
	        return new PermissionAsk(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.command = source["command"];
	        this.tool = source["tool"];
	        this.detail = source["detail"];
	    }
	}
	export class TableRows {
	    table: string;
	    rows: number;
	
	    static createFrom(source: any = {}) {
	        return new TableRows(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.table = source["table"];
	        this.rows = source["rows"];
	    }
	}
	export class ViewedFile {
	    path: string;
	    blob: string;
	
	    static createFrom(source: any = {}) {
	        return new ViewedFile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.blob = source["blob"];
	    }
	}

}

export namespace tracker {
	
	export class Updates {
	    start: boolean;
	    result: boolean;
	    done: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Updates(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.start = source["start"];
	        this.result = source["result"];
	        this.done = source["done"];
	    }
	}
	export class Allowance {
	    writes: string[];
	    updates: Updates;
	
	    static createFrom(source: any = {}) {
	        return new Allowance(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.writes = source["writes"];
	        this.updates = this.convertValues(source["updates"], Updates);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace version {
	
	export class Info {
	    version: string;
	    commit: string;
	
	    static createFrom(source: any = {}) {
	        return new Info(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.commit = source["commit"];
	    }
	}

}

export namespace worktree {
	
	export class BranchDeletion {
	    branch: string;
	    commit: string;
	    unique: number;
	    worktree?: string;
	
	    static createFrom(source: any = {}) {
	        return new BranchDeletion(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.branch = source["branch"];
	        this.commit = source["commit"];
	        this.unique = source["unique"];
	        this.worktree = source["worktree"];
	    }
	}
	export class BranchSync {
	    name: string;
	    upstream?: string;
	    state: string;
	    ahead: number;
	    behind: number;
	    behindBase: number;
	    isBase?: boolean;
	    commit: string;
	    committedAt: string;
	    checkedOut?: string;
	    main?: boolean;
	    dirty?: boolean;
	    updatable: boolean;
	    reason?: string;
	    unique: number;
	    deletable: boolean;
	    deleteReason?: string;
	
	    static createFrom(source: any = {}) {
	        return new BranchSync(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.upstream = source["upstream"];
	        this.state = source["state"];
	        this.ahead = source["ahead"];
	        this.behind = source["behind"];
	        this.behindBase = source["behindBase"];
	        this.isBase = source["isBase"];
	        this.commit = source["commit"];
	        this.committedAt = source["committedAt"];
	        this.checkedOut = source["checkedOut"];
	        this.main = source["main"];
	        this.dirty = source["dirty"];
	        this.updatable = source["updatable"];
	        this.reason = source["reason"];
	        this.unique = source["unique"];
	        this.deletable = source["deletable"];
	        this.deleteReason = source["deleteReason"];
	    }
	}
	export class BranchUpdate {
	    branch: string;
	    upstream: string;
	    from: string;
	    to: string;
	    checkedOut?: string;
	    kept?: number;
	    settled?: string[];
	
	    static createFrom(source: any = {}) {
	        return new BranchUpdate(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.branch = source["branch"];
	        this.upstream = source["upstream"];
	        this.from = source["from"];
	        this.to = source["to"];
	        this.checkedOut = source["checkedOut"];
	        this.kept = source["kept"];
	        this.settled = source["settled"];
	    }
	}
	export class PendingChange {
	    status: string;
	    path: string;
	
	    static createFrom(source: any = {}) {
	        return new PendingChange(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = source["status"];
	        this.path = source["path"];
	    }
	}
	export class SettingsChange {
	    path: string;
	    kind: string;
	    onBase?: boolean;
	    conflict?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SettingsChange(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.kind = source["kind"];
	        this.onBase = source["onBase"];
	        this.conflict = source["conflict"];
	    }
	}
	export class SettingsStatus {
	    baseRef?: string;
	    changes: SettingsChange[];
	    digest?: string;
	    shareable: boolean;
	    reason?: string;
	    sharedOn?: string;
	
	    static createFrom(source: any = {}) {
	        return new SettingsStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.baseRef = source["baseRef"];
	        this.changes = this.convertValues(source["changes"], SettingsChange);
	        this.digest = source["digest"];
	        this.shareable = source["shareable"];
	        this.reason = source["reason"];
	        this.sharedOn = source["sharedOn"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SyncStatus {
	    repository: string;
	    hasOrigin: boolean;
	    origin?: string;
	    base?: string;
	    baseRef?: string;
	    fetchedAt?: string;
	    branches: BranchSync[];
	    truncated?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SyncStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.repository = source["repository"];
	        this.hasOrigin = source["hasOrigin"];
	        this.origin = source["origin"];
	        this.base = source["base"];
	        this.baseRef = source["baseRef"];
	        this.fetchedAt = source["fetchedAt"];
	        this.branches = this.convertValues(source["branches"], BranchSync);
	        this.truncated = source["truncated"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SyncSummary {
	    hasOrigin: boolean;
	    origin?: string;
	    base?: string;
	    fetchedAt?: string;
	    checkout?: BranchSync;
	    branches: number;
	    outOfSync: number;
	    noUpstream: number;
	
	    static createFrom(source: any = {}) {
	        return new SyncSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hasOrigin = source["hasOrigin"];
	        this.origin = source["origin"];
	        this.base = source["base"];
	        this.fetchedAt = source["fetchedAt"];
	        this.checkout = this.convertValues(source["checkout"], BranchSync);
	        this.branches = source["branches"];
	        this.outOfSync = source["outOfSync"];
	        this.noUpstream = source["noUpstream"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace yaml {
	
	export class Node {
	    Kind: number;
	    Style: number;
	    Tag: string;
	    Value: string;
	    Anchor: string;
	    Alias?: Node;
	    Content: Node[];
	    HeadComment: string;
	    LineComment: string;
	    FootComment: string;
	    Line: number;
	    Column: number;
	
	    static createFrom(source: any = {}) {
	        return new Node(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Kind = source["Kind"];
	        this.Style = source["Style"];
	        this.Tag = source["Tag"];
	        this.Value = source["Value"];
	        this.Anchor = source["Anchor"];
	        this.Alias = this.convertValues(source["Alias"], Node);
	        this.Content = this.convertValues(source["Content"], Node);
	        this.HeadComment = source["HeadComment"];
	        this.LineComment = source["LineComment"];
	        this.FootComment = source["FootComment"];
	        this.Line = source["Line"];
	        this.Column = source["Column"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

