package com.supportchat.app;
import org.junit.Test;
import static org.junit.Assert.*;
public class ConnectionResolverTest {
    final String id="0123456789abcdef";
    final ConnectionResolver.State old=new ConnectionResolver.State("https://old.example",id,3);
    ConnectionResolver.Fetcher source(long revision,String identity,String address,boolean ready){return new ConnectionResolver.Fetcher(){
        public ConnectionResolver.Route route(String directory){assertEquals("https://config.example",directory);return new ConnectionResolver.Route(1,id,revision,address);}
        public ConnectionResolver.Identity identity(String server){return new ConnectionResolver.Identity(1,identity,ready);}
    };}
    @Test public void canonicalOrigin(){assertEquals("https://example.com",ConnectionResolver.origin("https://EXAMPLE.com:443/",false));}
    @Test public void unsafeOrigins(){for(String s:new String[]{"http://example.com","https://user@example.com","https://example.com/a","https://example.com?","https://example.com#","https://example.com:0","https://example.com:65536","https://example.com:","https://example.com\\@evil.test","javascript:alert(1)"})assertThrows(s,IllegalArgumentException.class,()->ConnectionResolver.origin(s,false));}
    @Test public void debugLoopbackOnly(){assertEquals("http://10.0.2.2:8095",ConnectionResolver.origin("http://10.0.2.2:8095",true));assertThrows(IllegalArgumentException.class,()->ConnectionResolver.origin("http://192.168.1.1",true));assertThrows(IllegalArgumentException.class,()->ConnectionResolver.origin("http://127.0.0.1",false));}
    @Test public void exactOrigin(){assertTrue(ConnectionResolver.sameOrigin("https://example.com","https://example.com:443/customer",false));assertFalse(ConnectionResolver.sameOrigin("https://example.com","https://example.com.evil.test",false));assertFalse(ConnectionResolver.sameOrigin("https://example.com","https://example.com:444",false));assertFalse(ConnectionResolver.sameOrigin("https://example.com","https://u@example.com",false));}
    @Test public void migratedService() throws Exception {var next=ConnectionResolver.resolve("https://config.example",old,source(4,id,"https://new.example",true),false);assertEquals("https://new.example",next.serverUrl());assertEquals(4,next.revision());}
    @Test public void wrongIdentity(){assertThrows(IllegalArgumentException.class,()->ConnectionResolver.resolve("https://config.example",old,source(4,"different-service-id","https://new.example",true),false));}
    @Test public void staleOrReusedRevision(){for(long revision:new long[]{2,3})assertThrows(IllegalArgumentException.class,()->ConnectionResolver.resolve("https://config.example",old,source(revision,id,"https://new.example",true),false));}
    @Test public void unavailableDoesNotMutateCachedState(){assertThrows(IllegalArgumentException.class,()->ConnectionResolver.resolve("https://config.example",old,source(4,id,"https://new.example",false),false));assertEquals("https://old.example",old.serverUrl());assertEquals(3,old.revision());}
    @Test public void firstInstallAndRepeatedCheck() throws Exception {var first=ConnectionResolver.resolve("https://config.example",new ConnectionResolver.State("","",0),source(3,id,"https://old.example",true),false);assertEquals(old,first);assertEquals(old,ConnectionResolver.resolve("https://config.example",first,source(3,id,"https://old.example",true),false));}
}
