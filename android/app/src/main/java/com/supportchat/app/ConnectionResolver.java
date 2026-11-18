package com.supportchat.app;

import java.net.URI;
import java.util.Locale;

/** Only public connection metadata passes through this resolver. Never accept login credentials. */
public final class ConnectionResolver {
    public record Route(int protocol, String serviceId, long revision, String serverUrl) {}
    public record Identity(int protocol, String serviceId, boolean ready) {}
    public record State(String serverUrl, String serviceId, long revision) {}
    public interface Fetcher {
        Route route(String directory) throws Exception;
        Identity identity(String server) throws Exception;
    }
    public static String origin(String value, boolean local) {
        try {
            URI u = new URI(value);
            String host=u.getHost(), scheme=u.getScheme();
            if(host==null || scheme==null || u.getRawUserInfo()!=null || u.getRawQuery()!=null || u.getRawFragment()!=null
                || !(u.getRawPath().isEmpty() || u.getRawPath().equals("/")) || u.getPort()==0 || u.getPort()>65535
                || u.getRawAuthority().endsWith(":")) throw new IllegalArgumentException();
            host=host.toLowerCase(Locale.ROOT); scheme=scheme.toLowerCase(Locale.ROOT);
            if(!scheme.equals("https") && !(local && scheme.equals("http") && (host.equals("127.0.0.1") || host.equals("localhost") || host.equals("10.0.2.2")))) throw new IllegalArgumentException();
            int port=u.getPort();
            return scheme+"://"+host+((port==-1 || (scheme.equals("https")&&port==443) || (scheme.equals("http")&&port==80)) ? "" : ":"+port);
        } catch(Exception e) { throw new IllegalArgumentException("请输入有效的 HTTPS 域名；调试版支持本机 HTTP",e); }
    }
    public static boolean sameOrigin(String base,String target,boolean local) {
        try {
            URI u=new URI(target);
            if(u.getRawUserInfo()!=null)return false;
            return origin(base,local).equals(origin(u.getScheme()+"://"+u.getRawAuthority(),local));
        } catch(Exception e) {return false;}
    }
    public static State resolve(String directory,State previous,Fetcher fetcher,boolean local) throws Exception {
        Route r=fetcher.route(origin(directory,local));
        if(r.protocol()!=1 || r.revision()<1 || r.serviceId()==null || r.serviceId().length()<16 || r.serviceId().length()>80)throw new IllegalArgumentException("配置入口格式不兼容");
        String server=origin(r.serverUrl(),local);
        if(!previous.serviceId().isEmpty() && !previous.serviceId().equals(r.serviceId()))throw new IllegalArgumentException("服务身份不一致");
        if(r.revision()<previous.revision() || (r.revision()==previous.revision() && !previous.serverUrl().isEmpty() && !server.equals(origin(previous.serverUrl(),local))))throw new IllegalArgumentException("配置版本已过期");
        Identity id=fetcher.identity(server);
        if(!id.ready() || id.protocol()!=1 || !r.serviceId().equals(id.serviceId()))throw new IllegalArgumentException("新服务尚未通过身份和就绪校验");
        return new State(server,r.serviceId(),r.revision());
    }
}
