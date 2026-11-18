export interface User { id: string; username: string; name: string; role: 'agent' | 'customer'; disabled: boolean; unread: number; lastActive: number }
export interface Attachment { id: string; name: string; mime: string; kind: 'image' | 'voice' | 'file'; size: number; duration: number }
export interface Message { id: string; seq: number; kind: string; text?: string; attachment?: Attachment; sender: string; sentAt: number }
export interface History { messages: Message[]; before: number; maxSeq: number }
export interface LibraryBlock { kind: 'text' | 'image'; text?: string; imageId?: string }
export interface LibrarySection { title: string; blocks: LibraryBlock[] }
export interface LibraryEntry { id: string; kind: 'reply' | 'intro'; title: string; category: string; sections: LibrarySection[]; version: number; updated: number }
export interface LibraryDelivery { id: string; title: string; total: number; sent: number; created: number; uncertain: boolean }
